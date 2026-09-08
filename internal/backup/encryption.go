package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	encryptedArchiveFormatVersion = 1
	encryptedArchiveMagic         = "XBOARD-GO-BACKUP-ENC\n"
	encryptedArchiveAlgorithm     = "AES-256-GCM-CHUNKED-SHA256"
	encryptedArchiveKDF           = "direct-32-byte-key"
	encryptedArchiveChunkSize     = 4 << 20
	maxEncryptedHeaderBytes       = 64 << 10
	maxEncryptedArchiveBytes      = maxArchiveBytes + (maxArchiveBytes/encryptedArchiveChunkSize+1)*(aesGCMOverhead+4) + maxEncryptedHeaderBytes + int64(len(encryptedArchiveMagic)+4)
	aesGCMOverhead                = 16
)

type EncryptedManifest struct {
	FormatVersion   int       `json:"format_version"`
	CreatedAt       time.Time `json:"created_at"`
	Algorithm       string    `json:"algorithm"`
	KDF             string    `json:"kdf"`
	ChunkSize       int       `json:"chunk_size"`
	PlaintextSize   int64     `json:"plaintext_size"`
	PlaintextSHA256 string    `json:"plaintext_sha256"`
	NoncePrefix     string    `json:"nonce_prefix"`
	BackupManifest  Manifest  `json:"backup_manifest"`
}

func Encrypt(ctx context.Context, inputPath, outputPath string, key []byte, now time.Time) (EncryptedManifest, error) {
	if err := ctx.Err(); err != nil {
		return EncryptedManifest{}, err
	}
	aead, err := backupAEAD(key)
	if err != nil {
		return EncryptedManifest{}, err
	}
	if now.IsZero() {
		return EncryptedManifest{}, errors.New("encrypted backup creation time is required")
	}
	plainManifest, err := Verify(ctx, inputPath)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("verify plaintext backup before encryption: %w", err)
	}
	inputInfo, err := os.Lstat(strings.TrimSpace(inputPath))
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("inspect plaintext backup archive: %w", err)
	}
	if !inputInfo.Mode().IsRegular() || inputInfo.Size() <= 0 || inputInfo.Size() > maxArchiveBytes {
		return EncryptedManifest{}, errors.New("plaintext backup archive must be a non-empty regular file within the size limit")
	}
	plainDigest, err := fileSHA256(ctx, inputPath)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("hash plaintext backup archive: %w", err)
	}
	outputPath, err = prepareNewOutputPath(outputPath)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("prepare encrypted backup output: %w", err)
	}
	noncePrefix := make([]byte, 4)
	if _, err := rand.Read(noncePrefix); err != nil {
		return EncryptedManifest{}, fmt.Errorf("generate encrypted backup nonce prefix: %w", err)
	}
	manifest := EncryptedManifest{
		FormatVersion: encryptedArchiveFormatVersion, CreatedAt: now.UTC(), Algorithm: encryptedArchiveAlgorithm, KDF: encryptedArchiveKDF,
		ChunkSize: encryptedArchiveChunkSize, PlaintextSize: inputInfo.Size(), PlaintextSHA256: plainDigest,
		NoncePrefix: hex.EncodeToString(noncePrefix), BackupManifest: plainManifest,
	}
	header, err := encodeEncryptedManifest(manifest)
	if err != nil {
		return EncryptedManifest{}, err
	}
	temporaryPath, err := unusedTemporaryPath(filepath.Dir(outputPath), ".xboard-backup-encrypted-*")
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("reserve encrypted backup path: %w", err)
	}
	defer os.Remove(temporaryPath)
	if err := writeEncryptedArchive(ctx, inputPath, inputInfo, temporaryPath, aead, noncePrefix, header); err != nil {
		return EncryptedManifest{}, err
	}
	if err := publishNoReplace(temporaryPath, outputPath); err != nil {
		return EncryptedManifest{}, fmt.Errorf("publish encrypted backup archive: %w", err)
	}
	verified, err := VerifyEncrypted(ctx, outputPath, key)
	if err != nil {
		_ = os.Remove(outputPath)
		return EncryptedManifest{}, fmt.Errorf("verify encrypted backup after publication: %w", err)
	}
	if verified != manifest {
		_ = os.Remove(outputPath)
		return EncryptedManifest{}, errors.New("encrypted backup manifest changed after publication")
	}
	return manifest, nil
}

func VerifyEncrypted(ctx context.Context, inputPath string, key []byte) (EncryptedManifest, error) {
	directory, err := os.MkdirTemp("", "xboard-backup-encrypted-verify-")
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("create encrypted backup verification directory: %w", err)
	}
	defer os.RemoveAll(directory)
	plainPath := filepath.Join(directory, "verified.xbbackup")
	manifest, err := decryptToPath(ctx, inputPath, plainPath, key)
	if err != nil {
		return EncryptedManifest{}, err
	}
	plainManifest, err := Verify(ctx, plainPath)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("verify decrypted backup archive: %w", err)
	}
	if plainManifest != manifest.BackupManifest {
		return EncryptedManifest{}, errors.New("decrypted backup manifest does not match encrypted metadata")
	}
	return manifest, nil
}

func Decrypt(ctx context.Context, inputPath, outputPath string, key []byte) (EncryptedManifest, error) {
	outputPath, err := prepareNewOutputPath(outputPath)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("prepare decrypted backup output: %w", err)
	}
	temporaryPath, err := unusedTemporaryPath(filepath.Dir(outputPath), ".xboard-backup-decrypted-*")
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("reserve decrypted backup path: %w", err)
	}
	defer os.Remove(temporaryPath)
	manifest, err := decryptToPath(ctx, inputPath, temporaryPath, key)
	if err != nil {
		return EncryptedManifest{}, err
	}
	plainManifest, err := Verify(ctx, temporaryPath)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("verify decrypted backup archive: %w", err)
	}
	if plainManifest != manifest.BackupManifest {
		return EncryptedManifest{}, errors.New("decrypted backup manifest does not match encrypted metadata")
	}
	if err := publishNoReplace(temporaryPath, outputPath); err != nil {
		return EncryptedManifest{}, fmt.Errorf("publish decrypted backup archive: %w", err)
	}
	return manifest, nil
}

func writeEncryptedArchive(ctx context.Context, inputPath string, inputInfo os.FileInfo, outputPath string, aead cipher.AEAD, noncePrefix []byte, header []byte) error {
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open plaintext backup archive: %w", err)
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened plaintext backup archive: %w", err)
	}
	if !os.SameFile(inputInfo, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Size() != inputInfo.Size() {
		return errors.New("plaintext backup archive changed while it was opened")
	}
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, backupFileMode)
	if err != nil {
		return fmt.Errorf("create encrypted backup archive: %w", err)
	}
	outputOpen := true
	defer func() {
		if outputOpen {
			_ = output.Close()
		}
	}()
	if err := writeEncryptedHeader(output, header); err != nil {
		return err
	}
	plain := make([]byte, encryptedArchiveChunkSize)
	var chunk uint64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := io.ReadFull(input, plain)
		if errors.Is(readErr, io.ErrUnexpectedEOF) || errors.Is(readErr, io.EOF) {
			if read == 0 {
				break
			}
		} else if readErr != nil {
			return fmt.Errorf("read plaintext backup archive: %w", readErr)
		}
		ciphertext := aead.Seal(nil, encryptedNonce(noncePrefix, chunk), plain[:read], header)
		if len(ciphertext) > encryptedArchiveChunkSize+aesGCMOverhead {
			return errors.New("encrypted backup chunk exceeds the supported size")
		}
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(ciphertext)))
		if _, err := output.Write(length[:]); err != nil {
			return fmt.Errorf("write encrypted backup chunk length: %w", err)
		}
		if _, err := output.Write(ciphertext); err != nil {
			return fmt.Errorf("write encrypted backup chunk: %w", err)
		}
		chunk++
		if errors.Is(readErr, io.ErrUnexpectedEOF) || errors.Is(readErr, io.EOF) {
			break
		}
	}
	var extra [1]byte
	if count, readErr := input.Read(extra[:]); count != 0 || !errors.Is(readErr, io.EOF) {
		return errors.New("plaintext backup archive changed while it was encrypted")
	}
	if err := output.Sync(); err != nil {
		return fmt.Errorf("sync encrypted backup archive: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close encrypted backup archive: %w", err)
	}
	outputOpen = false
	return nil
}

func decryptToPath(ctx context.Context, inputPath, outputPath string, key []byte) (EncryptedManifest, error) {
	if err := ctx.Err(); err != nil {
		return EncryptedManifest{}, err
	}
	aead, err := backupAEAD(key)
	if err != nil {
		return EncryptedManifest{}, err
	}
	inputInfo, err := os.Lstat(strings.TrimSpace(inputPath))
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("inspect encrypted backup archive: %w", err)
	}
	if !inputInfo.Mode().IsRegular() || inputInfo.Size() <= int64(len(encryptedArchiveMagic)+4) || inputInfo.Size() > maxEncryptedArchiveBytes {
		return EncryptedManifest{}, errors.New("encrypted backup archive must be a non-empty regular file within the size limit")
	}
	input, err := os.Open(inputPath)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("open encrypted backup archive: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, backupFileMode)
	if err != nil {
		return EncryptedManifest{}, fmt.Errorf("create decrypted backup archive: %w", err)
	}
	outputSucceeded := false
	defer func() {
		_ = output.Close()
		if !outputSucceeded {
			_ = os.Remove(outputPath)
		}
	}()
	header, manifest, err := readEncryptedHeader(input)
	if err != nil {
		return EncryptedManifest{}, err
	}
	if err := validateEncryptedManifest(manifest); err != nil {
		return EncryptedManifest{}, err
	}
	noncePrefix, _ := hex.DecodeString(manifest.NoncePrefix)
	hash := sha256.New()
	plainBuffer := make([]byte, 0, encryptedArchiveChunkSize)
	var total int64
	var chunk uint64
	for {
		if err := ctx.Err(); err != nil {
			return EncryptedManifest{}, err
		}
		var length [4]byte
		read, readErr := io.ReadFull(input, length[:])
		if errors.Is(readErr, io.EOF) && read == 0 {
			break
		}
		if readErr != nil {
			return EncryptedManifest{}, fmt.Errorf("read encrypted backup chunk length: %w", readErr)
		}
		ciphertextLength := binary.BigEndian.Uint32(length[:])
		if ciphertextLength <= aesGCMOverhead || ciphertextLength > encryptedArchiveChunkSize+aesGCMOverhead {
			return EncryptedManifest{}, errors.New("encrypted backup chunk size is invalid")
		}
		ciphertext := make([]byte, ciphertextLength)
		if _, err := io.ReadFull(input, ciphertext); err != nil {
			return EncryptedManifest{}, fmt.Errorf("read encrypted backup chunk: %w", err)
		}
		plaintext, err := aead.Open(plainBuffer[:0], encryptedNonce(noncePrefix, chunk), ciphertext, header)
		if err != nil {
			return EncryptedManifest{}, errors.New("decrypt encrypted backup chunk")
		}
		if total > manifest.PlaintextSize-int64(len(plaintext)) {
			return EncryptedManifest{}, errors.New("encrypted backup plaintext exceeds the manifest size")
		}
		total += int64(len(plaintext))
		if _, err := output.Write(plaintext); err != nil {
			return EncryptedManifest{}, fmt.Errorf("write decrypted backup archive: %w", err)
		}
		if _, err := hash.Write(plaintext); err != nil {
			return EncryptedManifest{}, err
		}
		chunk++
	}
	if total != manifest.PlaintextSize || hex.EncodeToString(hash.Sum(nil)) != manifest.PlaintextSHA256 {
		return EncryptedManifest{}, errors.New("decrypted backup archive does not match encrypted metadata")
	}
	if err := output.Sync(); err != nil {
		return EncryptedManifest{}, fmt.Errorf("sync decrypted backup archive: %w", err)
	}
	if err := output.Close(); err != nil {
		return EncryptedManifest{}, fmt.Errorf("close decrypted backup archive: %w", err)
	}
	if err := os.Chmod(outputPath, backupFileMode); err != nil {
		return EncryptedManifest{}, fmt.Errorf("restrict decrypted backup permissions: %w", err)
	}
	outputSucceeded = true
	return manifest, nil
}

func writeEncryptedHeader(output io.Writer, header []byte) error {
	if _, err := output.Write([]byte(encryptedArchiveMagic)); err != nil {
		return fmt.Errorf("write encrypted backup magic: %w", err)
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(header)))
	if _, err := output.Write(length[:]); err != nil {
		return fmt.Errorf("write encrypted backup header length: %w", err)
	}
	if _, err := output.Write(header); err != nil {
		return fmt.Errorf("write encrypted backup header: %w", err)
	}
	return nil
}

func readEncryptedHeader(input io.Reader) ([]byte, EncryptedManifest, error) {
	magic := make([]byte, len(encryptedArchiveMagic))
	if _, err := io.ReadFull(input, magic); err != nil {
		return nil, EncryptedManifest{}, fmt.Errorf("read encrypted backup magic: %w", err)
	}
	if !bytes.Equal(magic, []byte(encryptedArchiveMagic)) {
		return nil, EncryptedManifest{}, errors.New("encrypted backup magic is invalid")
	}
	var length [4]byte
	if _, err := io.ReadFull(input, length[:]); err != nil {
		return nil, EncryptedManifest{}, fmt.Errorf("read encrypted backup header length: %w", err)
	}
	headerLength := binary.BigEndian.Uint32(length[:])
	if headerLength == 0 || headerLength > maxEncryptedHeaderBytes {
		return nil, EncryptedManifest{}, errors.New("encrypted backup header size is invalid")
	}
	header := make([]byte, headerLength)
	if _, err := io.ReadFull(input, header); err != nil {
		return nil, EncryptedManifest{}, fmt.Errorf("read encrypted backup header: %w", err)
	}
	var manifest EncryptedManifest
	decoder := json.NewDecoder(strings.NewReader(string(header)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, EncryptedManifest{}, fmt.Errorf("decode encrypted backup header: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, EncryptedManifest{}, errors.New("encrypted backup header contains trailing JSON values")
	}
	return header, manifest, nil
}

func encodeEncryptedManifest(manifest EncryptedManifest) ([]byte, error) {
	if err := validateEncryptedManifest(manifest); err != nil {
		return nil, err
	}
	header, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode encrypted backup header: %w", err)
	}
	if len(header) == 0 || len(header) > maxEncryptedHeaderBytes {
		return nil, errors.New("encrypted backup header exceeds the supported limit")
	}
	return header, nil
}

func validateEncryptedManifest(manifest EncryptedManifest) error {
	if manifest.FormatVersion != encryptedArchiveFormatVersion {
		return fmt.Errorf("unsupported encrypted backup format version %d", manifest.FormatVersion)
	}
	if manifest.CreatedAt.IsZero() {
		return errors.New("encrypted backup creation time is required")
	}
	if manifest.Algorithm != encryptedArchiveAlgorithm {
		return fmt.Errorf("unsupported encrypted backup algorithm %q", manifest.Algorithm)
	}
	if manifest.KDF != encryptedArchiveKDF {
		return fmt.Errorf("unsupported encrypted backup KDF %q", manifest.KDF)
	}
	if manifest.ChunkSize != encryptedArchiveChunkSize {
		return errors.New("encrypted backup chunk size is invalid")
	}
	if manifest.PlaintextSize <= 0 || manifest.PlaintextSize > maxArchiveBytes {
		return errors.New("encrypted backup plaintext size is outside the supported range")
	}
	if len(manifest.PlaintextSHA256) != sha256.Size*2 || manifest.PlaintextSHA256 != strings.ToLower(manifest.PlaintextSHA256) {
		return errors.New("encrypted backup plaintext SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(manifest.PlaintextSHA256); err != nil {
		return errors.New("encrypted backup plaintext SHA-256 is invalid")
	}
	noncePrefix, err := hex.DecodeString(manifest.NoncePrefix)
	if err != nil || len(noncePrefix) != 4 {
		return errors.New("encrypted backup nonce prefix is invalid")
	}
	return validateManifest(manifest.BackupManifest)
}

func backupAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("backup encryption key must be exactly 32 bytes")
	}
	allZero := true
	for _, value := range key {
		if value != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return nil, errors.New("backup encryption key must not be all zero bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize backup encryption cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func encryptedNonce(prefix []byte, chunk uint64) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint64(nonce[4:], chunk)
	return nonce
}
