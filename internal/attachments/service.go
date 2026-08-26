package attachments

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

var (
	ErrInvalidInput  = errors.New("invalid attachment input")
	ErrConflict      = errors.New("attachment conflict")
	ErrQuotaExceeded = errors.New("attachment quota exceeded")
	ErrHashMismatch  = errors.New("attachment hash mismatch")
	ErrExpired       = errors.New("attachment upload expired")
	ErrNotFound      = errors.New("attachment not found")
)

const (
	UploadInitialized = store.KnowledgeUploadInitialized
	UploadUploading   = store.KnowledgeUploadUploading
	UploadCompleting  = store.KnowledgeUploadCompleting
	UploadCompleted   = store.KnowledgeUploadCompleted
	AttachmentReady   = store.KnowledgeAttachmentReady
)

type Upload = store.KnowledgeAttachmentUpload
type Attachment = store.KnowledgeAttachment

type Options struct {
	Root           string
	SigningKey     []byte
	PanelURL       string
	ChunkSize      int64
	MaxFileSize    int64
	TotalQuota     int64
	SignedURLTTL   time.Duration
	DraftTTL       time.Duration
	TrashRetention time.Duration
	MaxPerArticle  int
}

type InitializeInput struct {
	OriginalName string
	Size         int64
	DraftToken   string
	SHA256       string
}

type ChunkResult struct {
	Idempotent      bool `json:"idempotent"`
	ReceivedChunks  int  `json:"received_chunks"`
	ReadyToComplete bool `json:"ready_to_complete"`
}

type Service struct {
	database       *store.Store
	root           string
	signingKey     []byte
	panelURL       *url.URL
	chunkSize      int64
	maxFileSize    int64
	totalQuota     int64
	signedURLTTL   time.Duration
	draftTTL       time.Duration
	trashRetention time.Duration
	maxPerArticle  int
	locks          keyedLocks
	cleanupMu      sync.Mutex
}

type keyedLocks struct {
	mu    sync.Mutex
	items map[string]*keyedLock
}

type keyedLock struct {
	mu   sync.Mutex
	refs int
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func New(database *store.Store, options Options) (*Service, error) {
	if database == nil || len(options.SigningKey) < 32 || options.ChunkSize < 1 || options.MaxFileSize < 1 ||
		options.TotalQuota < 1 || options.ChunkSize > options.MaxFileSize || options.SignedURLTTL <= 0 ||
		options.DraftTTL <= 0 || options.TrashRetention <= 0 || options.MaxPerArticle < 1 {
		return nil, ErrInvalidInput
	}
	panelURL, err := url.Parse(options.PanelURL)
	if err != nil || (panelURL.Scheme != "http" && panelURL.Scheme != "https") || panelURL.Host == "" || panelURL.User != nil {
		return nil, fmt.Errorf("%w: invalid panel URL", ErrInvalidInput)
	}
	root, err := secureStorageRoot(options.Root)
	if err != nil {
		return nil, err
	}
	keyCopy := append([]byte(nil), options.SigningKey...)
	return &Service{
		database: database, root: root, signingKey: keyCopy, panelURL: panelURL,
		chunkSize: options.ChunkSize, maxFileSize: options.MaxFileSize, totalQuota: options.TotalQuota,
		signedURLTTL: options.SignedURLTTL, draftTTL: options.DraftTTL,
		trashRetention: options.TrashRetention, maxPerArticle: options.MaxPerArticle,
		locks: keyedLocks{items: make(map[string]*keyedLock)},
	}, nil
}

func (s *Service) Initialize(ctx context.Context, uploaderUserID int64, input InitializeInput, now time.Time) (Upload, error) {
	name, err := normalizeOriginalName(input.OriginalName)
	if err != nil || uploaderUserID < 1 || input.Size < 1 || input.Size > s.maxFileSize || now.Unix() < 0 {
		return Upload{}, ErrInvalidInput
	}
	draftDigest, err := digestDraftToken(input.DraftToken)
	if err != nil {
		return Upload{}, err
	}
	var expectedDigest *string
	if input.SHA256 != "" {
		if !sha256Pattern.MatchString(input.SHA256) {
			return Upload{}, ErrInvalidInput
		}
		expectedDigest = &input.SHA256
	}
	uploadUUID := uuid.NewString()
	totalChunks := int((input.Size + s.chunkSize - 1) / s.chunkSize)
	temporaryPath := filepath.ToSlash(filepath.Join("temporary", fmt.Sprintf("%d", uploaderUserID), uploadUUID))
	upload, err := s.database.ReserveKnowledgeAttachmentUpload(ctx, store.CreateKnowledgeAttachmentUploadInput{
		UUID: uploadUUID, UploaderUserID: uploaderUserID, DraftTokenHash: draftDigest,
		OriginalName: name, DeclaredSize: input.Size, ExpectedSHA256: expectedDigest,
		ChunkSize: s.chunkSize, TotalChunks: totalChunks, TemporaryPath: temporaryPath,
		ExpiresAt: now.Add(s.draftTTL), TotalQuota: s.totalQuota,
	}, now)
	if errors.Is(err, store.ErrAttachmentQuotaExceeded) {
		return Upload{}, ErrQuotaExceeded
	}
	if err != nil {
		return Upload{}, mapStoreError(err)
	}
	return upload, nil
}

func (s *Service) Status(ctx context.Context, uploaderUserID int64, uploadUUID string, now time.Time) (Upload, error) {
	if _, valid := canonicalAttachmentUUID(uploadUUID); uploaderUserID < 1 || !valid {
		return Upload{}, ErrInvalidInput
	}
	upload, err := s.database.GetKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now)
	if err != nil {
		return Upload{}, mapStoreError(err)
	}
	if upload.Status != UploadCompleted && !now.Before(upload.ExpiresAt) {
		if upload.Status == UploadInitialized || upload.Status == UploadUploading || upload.Status == store.KnowledgeUploadFailed {
			upload, err = s.database.ExpireKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now)
			if err != nil {
				return Upload{}, mapStoreError(err)
			}
		}
	}
	return upload, nil
}

func (s *Service) StoreChunk(ctx context.Context, uploaderUserID int64, uploadUUID string, index int, claimedDigest string, source io.Reader, contentLength int64, now time.Time) (ChunkResult, error) {
	if _, valid := canonicalAttachmentUUID(uploadUUID); source == nil || uploaderUserID < 1 || !valid || index < 0 || !sha256Pattern.MatchString(claimedDigest) {
		return ChunkResult{}, ErrInvalidInput
	}
	unlock := s.locks.lock(uploadUUID)
	defer unlock()
	upload, err := s.Status(ctx, uploaderUserID, uploadUUID, now)
	if err != nil {
		return ChunkResult{}, err
	}
	if upload.Status != UploadInitialized && upload.Status != UploadUploading && upload.Status != store.KnowledgeUploadFailed {
		if upload.Status == store.KnowledgeUploadExpired {
			return ChunkResult{}, ErrExpired
		}
		return ChunkResult{}, ErrConflict
	}
	if index >= upload.TotalChunks {
		return ChunkResult{}, ErrInvalidInput
	}
	expectedSize := upload.ChunkSize
	if index == upload.TotalChunks-1 {
		expectedSize = upload.DeclaredSize - int64(index)*upload.ChunkSize
	}
	if contentLength != expectedSize {
		return ChunkResult{}, ErrInvalidInput
	}
	directory, err := s.safePath(filepath.ToSlash(filepath.Join(upload.TemporaryPath, "chunks")))
	if err != nil {
		return ChunkResult{}, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return ChunkResult{}, fmt.Errorf("create attachment chunk directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".incoming-*")
	if err != nil {
		return ChunkResult{}, fmt.Errorf("create attachment chunk staging file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(source, expectedSize+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		return ChunkResult{}, fmt.Errorf("store attachment chunk: %w", errors.Join(copyErr, closeErr))
	}
	if written != expectedSize || hex.EncodeToString(hasher.Sum(nil)) != claimedDigest {
		return ChunkResult{}, ErrHashMismatch
	}
	chunkPath := filepath.Join(directory, fmt.Sprintf("%d.part", index))
	created := false
	if existingDigest, existingSize, readErr := hashFile(chunkPath); readErr == nil {
		if existingSize != expectedSize || subtle.ConstantTimeCompare([]byte(existingDigest), []byte(claimedDigest)) != 1 {
			return ChunkResult{}, ErrConflict
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return ChunkResult{}, fmt.Errorf("inspect attachment chunk: %w", readErr)
	} else {
		if err := os.Rename(temporaryName, chunkPath); err != nil {
			return ChunkResult{}, fmt.Errorf("publish attachment chunk: %w", err)
		}
		created = true
	}
	updated, idempotent, err := s.database.RecordKnowledgeAttachmentChunk(ctx, uploaderUserID, uploadUUID, index, expectedSize, claimedDigest, now)
	if err != nil {
		if created {
			_ = os.Remove(chunkPath)
		}
		return ChunkResult{}, mapStoreError(err)
	}
	return ChunkResult{Idempotent: idempotent, ReceivedChunks: updated.ReceivedChunks, ReadyToComplete: updated.ReceivedChunks == updated.TotalChunks}, nil
}

func (s *Service) Complete(ctx context.Context, uploaderUserID int64, uploadUUID string, now time.Time) (Attachment, error) {
	if _, valid := canonicalAttachmentUUID(uploadUUID); uploaderUserID < 1 || !valid {
		return Attachment{}, ErrInvalidInput
	}
	unlock := s.locks.lock(uploadUUID)
	defer unlock()
	upload, err := s.Status(ctx, uploaderUserID, uploadUUID, now)
	if err != nil {
		return Attachment{}, err
	}
	if upload.Status == UploadCompleted {
		attachment, err := s.database.GetKnowledgeAttachmentByUpload(ctx, uploaderUserID, uploadUUID)
		return attachment, mapStoreError(err)
	}
	if upload.Status == store.KnowledgeUploadExpired {
		return Attachment{}, ErrExpired
	}
	if upload.Status != UploadCompleting {
		upload, err = s.database.BeginKnowledgeAttachmentCompletion(ctx, uploaderUserID, uploadUUID, now)
		if err != nil {
			return Attachment{}, mapStoreError(err)
		}
	}
	quarantineRelative := filepath.ToSlash(filepath.Join("quarantine", uploadUUID+".part"))
	quarantinePath, err := s.safePath(quarantineRelative)
	if err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, err)
	}
	if err := os.MkdirAll(filepath.Dir(quarantinePath), 0o700); err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, fmt.Errorf("create attachment quarantine: %w", err))
	}
	output, err := os.OpenFile(quarantinePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, fmt.Errorf("create quarantined attachment: %w", err))
	}
	hasher := sha256.New()
	written := int64(0)
	for index := 0; index < upload.TotalChunks && err == nil; index++ {
		var chunkPath string
		chunkPath, err = s.safePath(filepath.ToSlash(filepath.Join(upload.TemporaryPath, "chunks", fmt.Sprintf("%d.part", index))))
		if err != nil {
			break
		}
		var chunk *os.File
		chunk, err = os.Open(chunkPath)
		if err != nil {
			break
		}
		var copied int64
		copied, err = io.Copy(io.MultiWriter(output, hasher), chunk)
		written += copied
		err = errors.Join(err, chunk.Close())
	}
	if err == nil {
		err = output.Sync()
	}
	err = errors.Join(err, output.Close())
	if err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, fmt.Errorf("assemble attachment: %w", err), quarantinePath)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if written != upload.DeclaredSize || (upload.ExpectedSHA256 != nil && subtle.ConstantTimeCompare([]byte(digest), []byte(*upload.ExpectedSHA256)) != 1) {
		_ = os.Remove(quarantinePath)
		if resetErr := s.database.ResetKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now); resetErr != nil {
			return Attachment{}, fmt.Errorf("reset corrupt attachment: %w", resetErr)
		}
		if temporaryPath, pathErr := s.safePath(upload.TemporaryPath); pathErr == nil {
			_ = os.RemoveAll(temporaryPath)
		}
		return Attachment{}, ErrHashMismatch
	}
	mimeType, err := detectMIMEType(quarantinePath)
	if err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, err, quarantinePath)
	}
	extension := safeExtension(upload.OriginalName)
	filename := upload.UUID
	if extension != nil {
		filename += "." + *extension
	}
	storageRelative := filepath.ToSlash(filepath.Join("files", now.UTC().Format("2006"), now.UTC().Format("01"), filename))
	storagePath, err := s.safePath(storageRelative)
	if err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, err, quarantinePath)
	}
	if err := os.MkdirAll(filepath.Dir(storagePath), 0o700); err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, err, quarantinePath)
	}
	if err := os.Remove(storagePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, fmt.Errorf("remove stale attachment object: %w", err), quarantinePath)
	}
	if err := os.Rename(quarantinePath, storagePath); err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, fmt.Errorf("publish attachment: %w", err), quarantinePath)
	}
	attachment, err := s.database.FinishKnowledgeAttachmentUpload(ctx, store.CompleteKnowledgeAttachmentInput{
		UUID: upload.UUID, UploaderUserID: uploaderUserID, OriginalName: upload.OriginalName,
		StoragePath: storageRelative, MIMEType: mimeType, Extension: extension,
		Size: written, SHA256: digest,
	}, now)
	if err != nil {
		return Attachment{}, s.failCompletion(ctx, uploaderUserID, uploadUUID, now, mapStoreError(err), storagePath)
	}
	if temporaryPath, pathErr := s.safePath(upload.TemporaryPath); pathErr == nil {
		_ = os.RemoveAll(temporaryPath)
	}
	return attachment, nil
}

func (s *Service) failCompletion(ctx context.Context, uploaderUserID int64, uploadUUID string, now time.Time, cause error, removePaths ...string) error {
	for _, path := range removePaths {
		_ = os.Remove(path)
	}
	if err := s.database.ResetKnowledgeAttachmentUpload(ctx, uploaderUserID, uploadUUID, now); err != nil {
		return errors.Join(cause, fmt.Errorf("reset failed attachment completion: %w", err))
	}
	return cause
}

func (s *Service) Open(ctx context.Context, attachmentUUID string) (*os.File, Attachment, error) {
	if _, valid := canonicalAttachmentUUID(attachmentUUID); !valid {
		return nil, Attachment{}, ErrInvalidInput
	}
	attachment, err := s.database.GetReadyKnowledgeAttachment(ctx, attachmentUUID)
	if err != nil {
		return nil, Attachment{}, mapStoreError(err)
	}
	path, err := s.safePath(attachment.StoragePath)
	if err != nil {
		return nil, Attachment{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, Attachment{}, fmt.Errorf("open attachment object: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != attachment.Size {
		_ = file.Close()
		return nil, Attachment{}, fmt.Errorf("attachment object integrity check failed")
	}
	return file, attachment, nil
}

func (s *Service) safePath(relative string) (string, error) {
	clean, err := safeRelativePath(relative)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return "", fmt.Errorf("open attachment storage root: %w", err)
	}
	defer root.Close()
	if err := verifyRootPathComponents(root, clean); err != nil {
		return "", err
	}
	return filepath.Join(s.root, clean), nil
}

func safeRelativePath(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, "\\") {
		return "", ErrInvalidInput
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if !filepath.IsLocal(clean) {
		return "", ErrInvalidInput
	}
	components := strings.Split(clean, string(filepath.Separator))
	for index, component := range components {
		base := filepath.Base(component)
		if base == "." || base == ".." || base != component {
			return "", ErrInvalidInput
		}
		components[index] = base
	}
	return filepath.Join(components...), nil
}

func verifyRootPathComponents(root *os.Root, relative string) error {
	current := ""
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect attachment path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are forbidden in attachment storage", ErrInvalidInput)
		}
	}
	return nil
}

func secureStorageRoot(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", ErrInvalidInput
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve attachment root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	volumeRoot := filepath.Clean(filepath.VolumeName(absolute) + string(filepath.Separator))
	if absolute == volumeRoot {
		return "", fmt.Errorf("%w: attachment root must not be a filesystem root", ErrInvalidInput)
	}
	if info, statErr := os.Lstat(absolute); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: attachment root must not be a symbolic link", ErrInvalidInput)
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect attachment root: %w", statErr)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("create attachment root: %w", err)
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil || filepath.Clean(real) != filepath.Clean(absolute) {
		return "", fmt.Errorf("%w: attachment root resolves through a symbolic link", ErrInvalidInput)
	}
	probe, err := os.CreateTemp(absolute, ".xboard-attachment-probe-*")
	if err != nil {
		return "", fmt.Errorf("verify attachment root is writable: %w", err)
	}
	probeName := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probeName)
		return "", fmt.Errorf("verify attachment root is writable: %w", closeErr)
	}
	if err := os.Remove(probeName); err != nil {
		return "", fmt.Errorf("remove attachment root write probe: %w", err)
	}
	return absolute, nil
}

func normalizeOriginalName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 255 ||
		filepath.Base(value) != value || strings.ContainsAny(value, `/\\`) {
		return "", ErrInvalidInput
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", ErrInvalidInput
		}
	}
	return value, nil
}

func digestDraftToken(value string) (string, error) {
	value = strings.ToLower(value)
	if !sha256Pattern.MatchString(value) {
		return "", ErrInvalidInput
	}
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:]), nil
}

func canonicalAttachmentUUID(value string) (string, bool) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Variant() != uuid.RFC4122 || parsed.Version() < 1 || parsed.Version() > 5 {
		return "", false
	}
	return parsed.String(), true
}

func safeExtension(name string) *string {
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if extension == "" || len(extension) > 32 {
		return nil
	}
	for _, character := range extension {
		if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') {
			return nil
		}
	}
	return &extension
}

func detectMIMEType(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("inspect attachment type: %w", err)
	}
	defer file.Close()
	buffer := make([]byte, 512)
	read, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("inspect attachment type: %w", err)
	}
	value := strings.ToLower(http.DetectContentType(buffer[:read]))
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		return "application/octet-stream", nil
	}
	return mime.FormatMediaType(mediaType, parameters), nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return ErrConflict
	case errors.Is(err, store.ErrInvalidInput):
		return ErrInvalidInput
	case errors.Is(err, store.ErrAttachmentQuotaExceeded):
		return ErrQuotaExceeded
	default:
		return err
	}
}

func (locks *keyedLocks) lock(key string) func() {
	locks.mu.Lock()
	item := locks.items[key]
	if item == nil {
		item = &keyedLock{}
		locks.items[key] = item
	}
	item.refs++
	locks.mu.Unlock()
	item.mu.Lock()
	return func() {
		item.mu.Unlock()
		locks.mu.Lock()
		item.refs--
		if item.refs == 0 {
			delete(locks.items, key)
		}
		locks.mu.Unlock()
	}
}
