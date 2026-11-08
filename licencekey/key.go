// Package licencekey is the licence key format, shared by the app (which only
// verifies) and the licence server (which signs).
//
// Wire format: SDS200-1-<base32(payload)>-<base32(signature)>, upper case, so
// a key can be read out over the air or copied by hand. The payload is packed
// binary rather than JSON to keep keys short enough to type.
package licencekey

import (
	"crypto/ed25519"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	Prefix     = "SDS200-1"
	ProductTag = "sds200-webapp"
)

var Encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Licence is the payload inside a key.
type Licence struct {
	Product  string    `json:"p"`
	Licensee string    `json:"n"`
	Email    string    `json:"e,omitempty"`
	Issued   time.Time `json:"i"`
	Expires  time.Time `json:"x,omitempty"` // zero means perpetual
	Order    string    `json:"o,omitempty"` // PayPal order id, for support
}

// Sign builds a key for a licence.
func Sign(priv ed25519.PrivateKey, lic Licence) (string, error) {
	lic.Product = ProductTag
	payload, err := Encode(lic)
	if err != nil {
		return "", err
	}
	body := Encoding.EncodeToString(payload) + "-" + Encoding.EncodeToString(ed25519.Sign(priv, payload))
	return Prefix + "-" + body, nil
}

// Verify checks a key against a hex-encoded Ed25519 public key.
func Verify(publicKeyHex, key string) (*Licence, error) {
	key = strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(key)), " ", "")
	if !strings.HasPrefix(key, strings.ToUpper(Prefix)+"-") {
		return nil, errors.New("that does not look like an SDS200 licence key")
	}
	parts := strings.Split(key[len(Prefix)+1:], "-")
	if len(parts) != 2 {
		return nil, errors.New("the key is incomplete")
	}
	payload, err := Encoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("the key is damaged")
	}
	sig, err := Encoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("the key is damaged")
	}
	if publicKeyHex == "" {
		return nil, errors.New("this build has no licence public key, so keys cannot be checked")
	}
	pub, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("this build has a malformed licence public key")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, sig) {
		return nil, errors.New("the key is not valid for this product")
	}
	lic, err := Decode(payload)
	if err != nil {
		return nil, err
	}
	if lic.Product != ProductTag {
		return nil, errors.New("that key is for a different product")
	}
	return lic, nil
}

func Encode(l Licence) ([]byte, error) {
	if strings.TrimSpace(l.Licensee) == "" {
		return nil, errors.New("a licence needs a licensee name")
	}
	b := []byte{1}
	b = appendString(b, l.Product)
	b = appendString(b, l.Licensee)
	b = appendString(b, l.Email)
	b = appendString(b, l.Order)
	b = binary.BigEndian.AppendUint32(b, uint32(l.Issued.Unix()))
	var exp uint32
	if !l.Expires.IsZero() {
		exp = uint32(l.Expires.Unix())
	}
	return binary.BigEndian.AppendUint32(b, exp), nil
}

func Decode(b []byte) (*Licence, error) {
	if len(b) < 1 || b[0] != 1 {
		return nil, errors.New("unsupported key version")
	}
	p := b[1:]
	var l Licence
	var err error
	for _, f := range []*string{&l.Product, &l.Licensee, &l.Email, &l.Order} {
		if *f, p, err = takeString(p); err != nil {
			return nil, err
		}
	}
	if len(p) < 8 {
		return nil, errors.New("the key is truncated")
	}
	l.Issued = time.Unix(int64(binary.BigEndian.Uint32(p[:4])), 0)
	if exp := binary.BigEndian.Uint32(p[4:8]); exp != 0 {
		l.Expires = time.Unix(int64(exp), 0)
	}
	return &l, nil
}

func appendString(b []byte, s string) []byte {
	if len(s) > 255 {
		s = s[:255]
	}
	return append(append(b, byte(len(s))), s...)
}

func takeString(b []byte) (string, []byte, error) {
	if len(b) < 1 {
		return "", nil, errors.New("the key is truncated")
	}
	n := int(b[0])
	if len(b) < 1+n {
		return "", nil, errors.New("the key is truncated")
	}
	return string(b[1 : 1+n]), b[1+n:], nil
}
