package embedding

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

const profileIDVersion = "embedding-profile-v1"

type Profile struct {
	Provider          string
	Model             string
	ProjectionVersion string
	ChunkerVersion    string
	Representation    string
	Normalization     string
	Dimensions        int
}

func (p Profile) ID() (string, error) {
	p = p.normalized()
	if err := p.Validate(); err != nil {
		return "", err
	}
	h := sha256.New()
	for _, value := range []string{
		profileIDVersion,
		p.Provider,
		p.Model,
		p.ProjectionVersion,
		p.ChunkerVersion,
		p.Representation,
		p.Normalization,
		strconv.Itoa(p.Dimensions),
	} {
		writeProfileField(h, value)
	}
	return profileIDVersion + ":" + hex.EncodeToString(h.Sum(nil)), nil
}

func (p Profile) Validate() error {
	p = p.normalized()
	if p.Provider == "" {
		return fmt.Errorf("embedding profile provider is required")
	}
	if p.Model == "" {
		return fmt.Errorf("embedding profile model is required")
	}
	if p.ProjectionVersion == "" {
		return fmt.Errorf("embedding profile projection version is required")
	}
	if p.ChunkerVersion == "" {
		return fmt.Errorf("embedding profile chunker version is required")
	}
	if p.Representation != RepresentationDenseF32 {
		return fmt.Errorf("unsupported embedding representation %q", p.Representation)
	}
	if p.Normalization != NormalizationL2 && p.Normalization != NormalizationNone {
		return fmt.Errorf("unsupported embedding normalization %q", p.Normalization)
	}
	if p.Dimensions <= 0 {
		return fmt.Errorf("embedding profile dimensions must be positive")
	}
	return nil
}

func (p Profile) normalized() Profile {
	p.Provider = strings.TrimSpace(p.Provider)
	p.Model = strings.TrimSpace(p.Model)
	p.ProjectionVersion = strings.TrimSpace(p.ProjectionVersion)
	p.ChunkerVersion = strings.TrimSpace(p.ChunkerVersion)
	p.Representation = strings.TrimSpace(p.Representation)
	p.Normalization = strings.TrimSpace(p.Normalization)
	return p
}

func writeProfileField(h hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(value))
}
