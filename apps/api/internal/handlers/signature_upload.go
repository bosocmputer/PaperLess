package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"paperless-api/internal/storage"
)

// maxSignaturePNGBytes caps a decoded signature image. A finger-drawn PNG is a
// few KB–tens of KB; 3 MB is a generous ceiling that still rejects abuse.
const maxSignaturePNGBytes = 3 << 20

var pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// decodeAndStoreSignature validates a base64-encoded PNG signature, uploads it
// to object storage, and returns the object key, byte size, and SHA-256 hex of
// the raw image bytes (server-computed — authoritative). On any problem it
// returns a machine-readable errCode (non-empty) and no side effects beyond a
// possible orphan object (harmless; never linked in the DB).
//
// Accepts either a bare base64 string or a full data URL ("data:image/png;base64,...").
func decodeAndStoreSignature(
	ctx context.Context, store *storage.Client, docID, taskID int64, b64 string,
) (objectKey string, size int64, hashHex string, errCode string) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return "", 0, "", "signature_required"
	}
	if strings.HasPrefix(b64, "data:") {
		if i := strings.Index(b64, ","); i != -1 {
			b64 = b64[i+1:]
		}
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", 0, "", "invalid_request"
	}
	if len(raw) == 0 {
		return "", 0, "", "signature_required"
	}
	if len(raw) > maxSignaturePNGBytes {
		return "", 0, "", "signature_too_large"
	}
	if len(raw) < len(pngMagic) || !bytes.Equal(raw[:len(pngMagic)], pngMagic) {
		return "", 0, "", "invalid_request" // not a PNG
	}

	sum := sha256.Sum256(raw)
	hashHex = hex.EncodeToString(sum[:])
	objectKey = fmt.Sprintf("signatures/doc-%d/task-%d-%s.png", docID, taskID, hashHex[:16])

	if err := store.Put(ctx, objectKey, "image/png", bytes.NewReader(raw), int64(len(raw))); err != nil {
		return "", 0, "", "storage_error"
	}
	return objectKey, int64(len(raw)), hashHex, ""
}
