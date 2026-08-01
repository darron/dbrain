// Package semanticsegment owns immutable, content-addressed ANN cache artifacts.
package semanticsegment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	SchemaVersion    = 1
	PayloadFileName  = "payload.bin"
	ManifestFileName = "manifest.json"
	RootFileName     = "root.json"
)

// Member maps a dense, segment-local ordinal to the exact SQLite embedding
// revision from which the opaque payload was built.
type Member struct {
	Ordinal    uint64 `json:"ordinal"`
	ChunkID    string `json:"chunk_id"`
	Revision   int64  `json:"revision"`
	VectorHash string `json:"vector_hash"`
}

type SegmentInput struct {
	DatabaseID, ProfileID, Backend, BackendVersion, DistanceMetric string
	Dimensions                                                     int
	Members                                                        []Member
	Payload                                                        func(io.Writer) error
}

type segmentDescriptor struct {
	SchemaVersion, Dimensions                                      int
	DatabaseID, ProfileID, Backend, BackendVersion, DistanceMetric string
	MemberCount                                                    int
	PayloadSHA256, MembersSHA256                                   string
}

type Manifest struct {
	SchemaVersion    int      `json:"schema_version"`
	DatabaseID       string   `json:"database_id"`
	ProfileID        string   `json:"profile_id"`
	Backend          string   `json:"backend"`
	BackendVersion   string   `json:"backend_version"`
	DistanceMetric   string   `json:"distance_metric"`
	Dimensions       int      `json:"dimensions"`
	Members          []Member `json:"members"`
	PayloadSHA256    string   `json:"payload_sha256"`
	MembersSHA256    string   `json:"members_sha256"`
	DescriptorSHA256 string   `json:"descriptor_sha256"`
}

type Segment struct {
	Hash         string
	RelativePath string
	Manifest     Manifest
}

type RootSegment struct {
	Hash         string `json:"hash"`
	RelativePath string `json:"relative_path"`
}

type RootInput struct {
	DatabaseID, ProfileID, GenerationID string
	SnapshotRevision, PurgeEpoch        int64
	Segments                            []RootSegment
}

type rootDescriptor struct {
	SchemaVersion                       int
	DatabaseID, ProfileID, GenerationID string
	SnapshotRevision, PurgeEpoch        int64
	Segments                            []RootSegment
}

type RootManifest struct {
	SchemaVersion    int           `json:"schema_version"`
	DatabaseID       string        `json:"database_id"`
	ProfileID        string        `json:"profile_id"`
	GenerationID     string        `json:"generation_id"`
	SnapshotRevision int64         `json:"snapshot_revision"`
	PurgeEpoch       int64         `json:"purge_epoch"`
	Segments         []RootSegment `json:"segments"`
	DescriptorSHA256 string        `json:"descriptor_sha256"`
}

type Root struct {
	RelativePath string
	Manifest     RootManifest
}

// RootDescriptorSHA256 returns the canonical root descriptor hash shared by
// filesystem publication and authoritative SQLite admission.
func RootDescriptorSHA256(input RootInput) (string, error) {
	if err := validateRootInput(input); err != nil {
		return "", err
	}
	descriptor := rootDescriptor{
		SchemaVersion: SchemaVersion, DatabaseID: input.DatabaseID, ProfileID: input.ProfileID,
		GenerationID: input.GenerationID, SnapshotRevision: input.SnapshotRevision, PurgeEpoch: input.PurgeEpoch,
		Segments: append([]RootSegment(nil), input.Segments...),
	}
	bytes, err := canonicalJSON(descriptor)
	if err != nil {
		return "", err
	}
	return sha256Hex(bytes), nil
}

func segmentRelativePath(databaseID, profileID, hash string) string {
	return filepath.ToSlash(filepath.Join("semantic", databaseID, profileID, "segments", hash))
}

func rootRelativePath(databaseID, profileID, generationID string) string {
	return filepath.ToSlash(filepath.Join("semantic", databaseID, profileID, "generations", generationID))
}

func validateSegmentInput(input SegmentInput) error {
	for _, field := range []struct{ name, value string }{
		{"database ID", input.DatabaseID}, {"profile ID", input.ProfileID}, {"backend", input.Backend},
		{"backend version", input.BackendVersion}, {"distance metric", input.DistanceMetric},
	} {
		if err := validatePathPart(field.name, field.value); err != nil {
			return err
		}
	}
	if input.Dimensions <= 0 {
		return fmt.Errorf("segment dimensions must be positive")
	}
	if input.Payload == nil {
		return fmt.Errorf("segment payload writer is required")
	}
	return validateMembers(input.Members)
}

func validateMembers(members []Member) error {
	if len(members) == 0 {
		return fmt.Errorf("segment members are required")
	}
	seen := make(map[string]struct{}, len(members))
	for index, member := range members {
		if member.Ordinal != uint64(index) {
			return fmt.Errorf("segment member %d ordinal %d must equal dense index", index, member.Ordinal)
		}
		if strings.TrimSpace(member.ChunkID) == "" {
			return fmt.Errorf("segment member %d chunk ID is required", index)
		}
		if _, exists := seen[member.ChunkID]; exists {
			return fmt.Errorf("segment member chunk ID %q is duplicated", member.ChunkID)
		}
		seen[member.ChunkID] = struct{}{}
		if member.Revision <= 0 {
			return fmt.Errorf("segment member %s revision must be positive", member.ChunkID)
		}
		if strings.TrimSpace(member.VectorHash) == "" {
			return fmt.Errorf("segment member %s vector hash is required", member.ChunkID)
		}
	}
	return nil
}

func validateRootInput(input RootInput) error {
	for _, field := range []struct{ name, value string }{
		{"database ID", input.DatabaseID}, {"profile ID", input.ProfileID}, {"generation ID", input.GenerationID},
	} {
		if err := validatePathPart(field.name, field.value); err != nil {
			return err
		}
	}
	if input.SnapshotRevision <= 0 {
		return fmt.Errorf("root snapshot revision must be positive")
	}
	if input.PurgeEpoch < 0 {
		return fmt.Errorf("root purge epoch cannot be negative")
	}
	if len(input.Segments) == 0 {
		return fmt.Errorf("root segments are required")
	}
	previous := ""
	for _, segment := range input.Segments {
		if err := validatePathPart("segment hash", segment.Hash); err != nil {
			return err
		}
		if segment.Hash <= previous {
			return fmt.Errorf("root segment hashes must be strictly sorted and unique")
		}
		previous = segment.Hash
		if segment.RelativePath != segmentRelativePath(input.DatabaseID, input.ProfileID, segment.Hash) {
			return fmt.Errorf("root segment %s has unexpected relative path %q", segment.Hash, segment.RelativePath)
		}
	}
	return nil
}

func validatePathPart(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value == "." || value == ".." || strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("%s %q is not a safe path part", name, value)
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			return fmt.Errorf("%s %q is not a safe path part", name, value)
		}
	}
	return nil
}

func canonicalJSON(value any) ([]byte, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical manifest: %w", err)
	}
	return bytes, nil
}

func sha256Hex(bytes []byte) string {
	digest := sha256.Sum256(bytes)
	return hex.EncodeToString(digest[:])
}
