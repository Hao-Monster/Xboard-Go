package attachments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const knowledgeAttachmentScheme = "knowledge-attachment://"

var nestedAttachmentLink = regexp.MustCompile(`(?i)(!?\[[^\r\n\]]*\])\(\s*!?\[[^\r\n\]]*\]\(\s*(knowledge-attachment://[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\s*\)\s*\)`)

// ParseKnowledgeReferences fails closed if the attachment scheme is present but
// is not followed by one canonical RFC 4122 UUID. It also deduplicates references
// while preserving their first-use order.
func ParseKnowledgeReferences(body string, maximum int) (string, []string, error) {
	if maximum < 1 || !utf8.ValidString(body) {
		return "", nil, ErrInvalidInput
	}
	for attempt := 0; attempt < 4; attempt++ {
		repaired := nestedAttachmentLink.ReplaceAllString(body, `$1($2)`)
		if repaired == body {
			break
		}
		body = repaired
	}
	lowerBody := strings.ToLower(body)
	var normalized strings.Builder
	normalized.Grow(len(body))
	references := make([]string, 0)
	seen := make(map[string]struct{})
	cursor := 0
	for {
		relative := strings.Index(lowerBody[cursor:], knowledgeAttachmentScheme)
		if relative < 0 {
			normalized.WriteString(body[cursor:])
			break
		}
		start := cursor + relative
		normalized.WriteString(body[cursor:start])
		identifierStart := start + len(knowledgeAttachmentScheme)
		identifierEnd := identifierStart + 36
		if identifierEnd > len(body) {
			return "", nil, ErrInvalidInput
		}
		rawIdentifier := body[identifierStart:identifierEnd]
		canonical, valid := canonicalAttachmentUUID(rawIdentifier)
		if !valid || len(rawIdentifier) != 36 || rawIdentifier[8] != '-' || rawIdentifier[13] != '-' || rawIdentifier[18] != '-' || rawIdentifier[23] != '-' {
			return "", nil, ErrInvalidInput
		}
		if identifierEnd < len(body) {
			next, _ := utf8.DecodeRuneInString(body[identifierEnd:])
			if !unicode.IsSpace(next) && !strings.ContainsRune(`)]}>"'`, next) {
				return "", nil, ErrInvalidInput
			}
		}
		normalized.WriteString(knowledgeAttachmentScheme)
		normalized.WriteString(canonical)
		if _, exists := seen[canonical]; !exists {
			seen[canonical] = struct{}{}
			references = append(references, canonical)
			if len(references) > maximum {
				return "", nil, ErrInvalidInput
			}
		}
		cursor = identifierEnd
	}
	return normalized.String(), references, nil
}

func (s *Service) CreateKnowledge(ctx context.Context, uploaderUserID int64, draftToken string, input store.SaveKnowledgeInput, now time.Time) (store.Knowledge, error) {
	normalizedBody, references, err := ParseKnowledgeReferences(input.Body, s.maxPerArticle)
	if err != nil {
		return store.Knowledge{}, err
	}
	input.Body = normalizedBody
	if len(references) == 0 && draftToken == "" {
		return s.database.CreateKnowledge(ctx, input, now)
	}
	draftDigest, err := digestDraftToken(draftToken)
	if err != nil {
		return store.Knowledge{}, err
	}
	item, err := s.database.CreateKnowledgeWithAttachments(ctx, input, uploaderUserID, draftDigest, references, now)
	return item, mapStoreError(err)
}

func (s *Service) UpdateKnowledge(ctx context.Context, uploaderUserID int64, draftToken string, knowledgeID, revision int64, input store.SaveKnowledgeInput, now time.Time) (store.Knowledge, error) {
	normalizedBody, references, err := ParseKnowledgeReferences(input.Body, s.maxPerArticle)
	if err != nil {
		return store.Knowledge{}, err
	}
	input.Body = normalizedBody
	draftDigest := emptyDraftDigest()
	if draftToken != "" {
		draftDigest, err = digestDraftToken(draftToken)
		if err != nil {
			return store.Knowledge{}, err
		}
	}
	item, err := s.database.UpdateKnowledgeWithAttachments(ctx, knowledgeID, revision, input, uploaderUserID, draftDigest, references, now)
	return item, mapStoreError(err)
}

func (s *Service) DeleteKnowledge(ctx context.Context, knowledgeID, revision int64, now time.Time) error {
	return mapStoreError(s.database.DeleteKnowledgeWithAttachments(ctx, knowledgeID, revision, now))
}

func (s *Service) RenderKnowledgeBody(ctx context.Context, knowledgeID int64, body string, public bool, now time.Time) (string, error) {
	normalized, references, err := ParseKnowledgeReferences(body, s.maxPerArticle)
	if err != nil {
		return "", err
	}
	if len(references) == 0 {
		return normalized, nil
	}
	available, err := s.database.GetKnowledgeAttachmentsForArticle(ctx, knowledgeID, references)
	if err != nil {
		return "", mapStoreError(err)
	}
	replacements := make([]string, 0, len(references)*2)
	for _, attachmentUUID := range references {
		attachment, exists := available[attachmentUUID]
		if !exists {
			return "", ErrNotFound
		}
		var replacement string
		if public {
			result := *s.panelURL
			result.Path = strings.TrimRight(result.Path, "/") + "/guide-attachments/" + attachment.UUID
			result.RawQuery = ""
			replacement = result.String()
		} else {
			replacement, err = s.SignedURL(attachment, "inline", now)
			if err != nil {
				return "", err
			}
		}
		replacements = append(replacements, knowledgeAttachmentScheme+attachmentUUID, replacement)
	}
	return strings.NewReplacer(replacements...).Replace(normalized), nil
}

func emptyDraftDigest() string {
	digest := sha256.Sum256(nil)
	return hex.EncodeToString(digest[:])
}
