package articlesearch

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"
)

type RetrievalMode byte

const (
	ModeBM25   RetrievalMode = 1
	ModeHybrid RetrievalMode = 2
)

type FrozenCandidate struct {
	ArticleID int64
	Identity  VectorIdentity
	Semantic  bool
}
type FrozenPage struct {
	Mode       RetrievalMode
	ExpiresAt  time.Time
	Offset     int
	Candidates []FrozenCandidate
}

func (c *CursorCodec) hybridCipher() (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, c.key, nil, "velis/article-search/cursor-v2", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (c *CursorCodec) EncodeFrozen(query Query, plan HybridConfig, state FrozenPage) (string, error) {
	if err := validateFrozen(state, plan, time.Time{}); err != nil {
		return "", err
	}
	var b bytes.Buffer
	q, _ := hex.DecodeString(QueryFingerprint(query))
	p, _ := hex.DecodeString(plan.Fingerprint())
	b.Write(q)
	b.Write(p)
	b.WriteByte(byte(state.Mode))
	_ = binary.Write(&b, binary.BigEndian, state.ExpiresAt.UnixNano())
	_ = binary.Write(&b, binary.BigEndian, uint16(state.Offset))
	_ = binary.Write(&b, binary.BigEndian, uint16(len(state.Candidates)))
	for _, item := range state.Candidates {
		_ = binary.Write(&b, binary.BigEndian, item.ArticleID)
		_ = binary.Write(&b, binary.BigEndian, item.Identity.RevisionID)
		for _, id := range []string{item.Identity.GenerationID, item.Identity.EmbeddingID} {
			raw := make([]byte, 16)
			if item.Semantic {
				decoded, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
				if err != nil || len(decoded) != 16 {
					return "", invalidFrozen()
				}
				copy(raw, decoded)
			}
			b.Write(raw)
		}
		flag := byte(0)
		if item.Semantic {
			flag = 1
		}
		b.WriteByte(flag)
	}
	aead, err := c.hybridCipher()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, b.Bytes(), []byte("2"))
	token := "2." + base64.RawURLEncoding.EncodeToString(sealed)
	if len(token) > maxCursorBytes {
		return "", invalidFrozen()
	}
	return token, nil
}

func (c *CursorCodec) DecodeFrozen(query Query, plan HybridConfig, token string, now time.Time) (FrozenPage, error) {
	if !strings.HasPrefix(token, "2.") || len(token) > maxCursorBytes {
		return FrozenPage{}, invalidFrozen()
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(token, "2."))
	if err != nil {
		return FrozenPage{}, invalidFrozen()
	}
	aead, err := c.hybridCipher()
	if err != nil {
		return FrozenPage{}, invalidFrozen()
	}
	if len(data) < aead.NonceSize()+aead.Overhead() {
		return FrozenPage{}, invalidFrozen()
	}
	raw, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("2"))
	if err != nil {
		return FrozenPage{}, invalidFrozen()
	}
	if len(raw) < 77 {
		return FrozenPage{}, invalidFrozen()
	}
	if hex.EncodeToString(raw[:32]) != QueryFingerprint(query) || hex.EncodeToString(raw[32:64]) != plan.Fingerprint() {
		return FrozenPage{}, invalidFrozen()
	}
	state := FrozenPage{Mode: RetrievalMode(raw[64]), ExpiresAt: time.Unix(0, int64(binary.BigEndian.Uint64(raw[65:73]))).UTC(), Offset: int(binary.BigEndian.Uint16(raw[73:75]))}
	count := int(binary.BigEndian.Uint16(raw[75:77]))
	if count > 200 || len(raw) != 77+count*49 {
		return FrozenPage{}, invalidFrozen()
	}
	reader := bytes.NewReader(raw[77:])
	state.Candidates = make([]FrozenCandidate, count)
	for i := range state.Candidates {
		item := &state.Candidates[i]
		_ = binary.Read(reader, binary.BigEndian, &item.ArticleID)
		_ = binary.Read(reader, binary.BigEndian, &item.Identity.RevisionID)
		g, e := make([]byte, 16), make([]byte, 16)
		_, _ = io.ReadFull(reader, g)
		_, _ = io.ReadFull(reader, e)
		flag, _ := reader.ReadByte()
		if flag > 1 {
			return FrozenPage{}, invalidFrozen()
		}
		item.Semantic = flag == 1
		if item.Semantic {
			item.Identity.GenerationID = formatUUID(g)
			item.Identity.EmbeddingID = formatUUID(e)
			item.Identity.Profile = plan.Profile
		} else if item.Identity.RevisionID != 0 || !bytes.Equal(g, make([]byte, 16)) || !bytes.Equal(e, make([]byte, 16)) {
			return FrozenPage{}, invalidFrozen()
		}
	}
	if err = validateFrozen(state, plan, now); err != nil {
		return FrozenPage{}, err
	}
	return state, nil
}

func formatUUID(raw []byte) string {
	s := hex.EncodeToString(raw)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func invalidFrozen() error {
	return controlled(CodeInvalidCursor, errors.New("混合游标状态无效"))
}
func validateFrozen(state FrozenPage, plan HybridConfig, now time.Time) error {
	if !plan.Enabled || (state.Mode != ModeBM25 && state.Mode != ModeHybrid) || state.ExpiresAt.IsZero() || (!now.IsZero() && (!now.Before(state.ExpiresAt) || state.ExpiresAt.After(now.Add(HybridTTL)))) || state.Offset < 0 || state.Offset >= len(state.Candidates) || len(state.Candidates) > 200 {
		return invalidFrozen()
	}
	seen := map[int64]bool{}
	for _, c := range state.Candidates {
		if c.ArticleID <= 0 || seen[c.ArticleID] {
			return invalidFrozen()
		}
		seen[c.ArticleID] = true
		if !c.Semantic && c.Identity != (VectorIdentity{}) {
			return invalidFrozen()
		}
		if c.Semantic && (state.Mode != ModeHybrid || !c.Identity.Matches(c.Identity) || c.Identity.Profile != plan.Profile) {
			return invalidFrozen()
		}
	}
	return nil
}
