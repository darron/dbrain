package semanticsegment

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func PublishSegment(cacheDir string, input SegmentInput) (Segment, error) {
	if err := validateSegmentInput(input); err != nil {
		return Segment{}, err
	}
	base := filepath.Join(cacheDir, "semantic", input.DatabaseID, input.ProfileID, "segments")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return Segment{}, fmt.Errorf("create semantic segment directory: %w", err)
	}
	temporary, err := os.MkdirTemp(base, ".segment-")
	if err != nil {
		return Segment{}, fmt.Errorf("create semantic segment temporary directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()

	payloadHash, err := writePayload(filepath.Join(temporary, PayloadFileName), input.Payload)
	if err != nil {
		return Segment{}, err
	}
	membersBytes, err := canonicalJSON(input.Members)
	if err != nil {
		return Segment{}, err
	}
	descriptor := segmentDescriptor{
		SchemaVersion: SchemaVersion, DatabaseID: input.DatabaseID, ProfileID: input.ProfileID,
		Backend: input.Backend, BackendVersion: input.BackendVersion, DistanceMetric: input.DistanceMetric,
		Dimensions: input.Dimensions, MemberCount: len(input.Members), PayloadSHA256: payloadHash,
		MembersSHA256: sha256Hex(membersBytes),
	}
	descriptorBytes, err := canonicalJSON(descriptor)
	if err != nil {
		return Segment{}, err
	}
	manifest := Manifest{
		SchemaVersion: SchemaVersion, DatabaseID: input.DatabaseID, ProfileID: input.ProfileID,
		Backend: input.Backend, BackendVersion: input.BackendVersion, DistanceMetric: input.DistanceMetric,
		Dimensions: input.Dimensions, Members: append([]Member(nil), input.Members...), PayloadSHA256: payloadHash,
		MembersSHA256: descriptor.MembersSHA256, DescriptorSHA256: sha256Hex(descriptorBytes),
	}
	manifestBytes, err := canonicalJSON(manifest)
	if err != nil {
		return Segment{}, err
	}
	if err := writeFileSync(filepath.Join(temporary, ManifestFileName), manifestBytes); err != nil {
		return Segment{}, err
	}
	if err := syncDirectory(temporary); err != nil {
		return Segment{}, err
	}

	hash := manifest.DescriptorSHA256
	final := filepath.Join(base, hash)
	if _, err := os.Stat(final); err == nil {
		segment, openErr := OpenSegment(cacheDir, input.DatabaseID, input.ProfileID, hash)
		if openErr != nil {
			return Segment{}, fmt.Errorf("validate existing semantic segment %s: %w", hash, openErr)
		}
		return segment, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Segment{}, fmt.Errorf("stat semantic segment destination: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return Segment{}, fmt.Errorf("publish semantic segment: %w", err)
	}
	published = true
	if err := syncDirectory(base); err != nil {
		return Segment{}, err
	}
	return Segment{Hash: hash, RelativePath: segmentRelativePath(input.DatabaseID, input.ProfileID, hash), Manifest: manifest}, nil
}

func OpenSegment(cacheDir, databaseID, profileID, hash string) (Segment, error) {
	if err := validatePathPart("database ID", databaseID); err != nil {
		return Segment{}, err
	}
	if err := validatePathPart("profile ID", profileID); err != nil {
		return Segment{}, err
	}
	if err := validatePathPart("segment hash", hash); err != nil {
		return Segment{}, err
	}
	directory := filepath.Join(cacheDir, filepath.FromSlash(segmentRelativePath(databaseID, profileID, hash)))
	manifestBytes, err := os.ReadFile(filepath.Join(directory, ManifestFileName))
	if err != nil {
		return Segment{}, fmt.Errorf("read semantic segment manifest: %w", err)
	}
	var manifest Manifest
	if err := decodeStrictJSON(manifestBytes, &manifest); err != nil {
		return Segment{}, fmt.Errorf("decode semantic segment manifest: %w", err)
	}
	if err := validateManifest(manifest, databaseID, profileID, hash); err != nil {
		return Segment{}, err
	}
	payload, err := os.ReadFile(filepath.Join(directory, PayloadFileName))
	if err != nil {
		return Segment{}, fmt.Errorf("read semantic segment payload: %w", err)
	}
	if sha256Hex(payload) != manifest.PayloadSHA256 {
		return Segment{}, fmt.Errorf("semantic segment %s payload checksum mismatch", hash)
	}
	return Segment{Hash: hash, RelativePath: segmentRelativePath(databaseID, profileID, hash), Manifest: manifest}, nil
}

func PublishRoot(cacheDir string, input RootInput) (Root, error) {
	if err := validateRootInput(input); err != nil {
		return Root{}, err
	}
	for _, segment := range input.Segments {
		if _, err := OpenSegment(cacheDir, input.DatabaseID, input.ProfileID, segment.Hash); err != nil {
			return Root{}, fmt.Errorf("validate root segment %s: %w", segment.Hash, err)
		}
	}
	base := filepath.Join(cacheDir, "semantic", input.DatabaseID, input.ProfileID, "generations")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return Root{}, fmt.Errorf("create semantic root directory: %w", err)
	}
	temporary, err := os.MkdirTemp(base, ".generation-")
	if err != nil {
		return Root{}, fmt.Errorf("create semantic root temporary directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporary)
		}
	}()
	descriptor := rootDescriptor{SchemaVersion: SchemaVersion, DatabaseID: input.DatabaseID, ProfileID: input.ProfileID,
		GenerationID: input.GenerationID, SnapshotRevision: input.SnapshotRevision, PurgeEpoch: input.PurgeEpoch,
		Segments: append([]RootSegment(nil), input.Segments...)}
	descriptorBytes, err := canonicalJSON(descriptor)
	if err != nil {
		return Root{}, err
	}
	manifest := RootManifest{SchemaVersion: SchemaVersion, DatabaseID: input.DatabaseID, ProfileID: input.ProfileID,
		GenerationID: input.GenerationID, SnapshotRevision: input.SnapshotRevision, PurgeEpoch: input.PurgeEpoch,
		Segments: append([]RootSegment(nil), input.Segments...), DescriptorSHA256: sha256Hex(descriptorBytes)}
	manifestBytes, err := canonicalJSON(manifest)
	if err != nil {
		return Root{}, err
	}
	if err := writeFileSync(filepath.Join(temporary, RootFileName), manifestBytes); err != nil {
		return Root{}, err
	}
	if err := syncDirectory(temporary); err != nil {
		return Root{}, err
	}
	final := filepath.Join(base, input.GenerationID)
	if _, err := os.Stat(final); err == nil {
		return Root{}, fmt.Errorf("semantic root generation already exists: %s", input.GenerationID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Root{}, fmt.Errorf("stat semantic root destination: %w", err)
	}
	if err := os.Rename(temporary, final); err != nil {
		return Root{}, fmt.Errorf("publish semantic root: %w", err)
	}
	published = true
	if err := syncDirectory(base); err != nil {
		return Root{}, err
	}
	return Root{RelativePath: rootRelativePath(input.DatabaseID, input.ProfileID, input.GenerationID), Manifest: manifest}, nil
}

func OpenRoot(cacheDir, databaseID, profileID, generationID string) (Root, error) {
	if err := validateRootInput(RootInput{DatabaseID: databaseID, ProfileID: profileID, GenerationID: generationID, SnapshotRevision: 1, Segments: []RootSegment{{Hash: "placeholder", RelativePath: segmentRelativePath(databaseID, profileID, "placeholder")}}}); err != nil {
		return Root{}, err
	}
	directory := filepath.Join(cacheDir, filepath.FromSlash(rootRelativePath(databaseID, profileID, generationID)))
	bytes, err := os.ReadFile(filepath.Join(directory, RootFileName))
	if err != nil {
		return Root{}, fmt.Errorf("read semantic root manifest: %w", err)
	}
	var manifest RootManifest
	if err := decodeStrictJSON(bytes, &manifest); err != nil {
		return Root{}, fmt.Errorf("decode semantic root manifest: %w", err)
	}
	if err := validateRootManifest(manifest, databaseID, profileID, generationID); err != nil {
		return Root{}, err
	}
	for _, segment := range manifest.Segments {
		if _, err := OpenSegment(cacheDir, databaseID, profileID, segment.Hash); err != nil {
			return Root{}, fmt.Errorf("open semantic root segment %s: %w", segment.Hash, err)
		}
	}
	return Root{RelativePath: rootRelativePath(databaseID, profileID, generationID), Manifest: manifest}, nil
}

func validateManifest(manifest Manifest, databaseID, profileID, hash string) error {
	input := SegmentInput{DatabaseID: manifest.DatabaseID, ProfileID: manifest.ProfileID, Backend: manifest.Backend,
		BackendVersion: manifest.BackendVersion, DistanceMetric: manifest.DistanceMetric, Dimensions: manifest.Dimensions,
		Members: manifest.Members, Payload: func(io.Writer) error { return nil }}
	if manifest.SchemaVersion != SchemaVersion || manifest.DatabaseID != databaseID || manifest.ProfileID != profileID {
		return fmt.Errorf("semantic segment %s identity mismatch", hash)
	}
	if err := validateSegmentInput(input); err != nil {
		return err
	}
	membersBytes, err := canonicalJSON(manifest.Members)
	if err != nil {
		return err
	}
	if sha256Hex(membersBytes) != manifest.MembersSHA256 {
		return fmt.Errorf("semantic segment %s membership checksum mismatch", hash)
	}
	descriptor := segmentDescriptor{SchemaVersion: manifest.SchemaVersion, DatabaseID: manifest.DatabaseID, ProfileID: manifest.ProfileID,
		Backend: manifest.Backend, BackendVersion: manifest.BackendVersion, DistanceMetric: manifest.DistanceMetric,
		Dimensions: manifest.Dimensions, MemberCount: len(manifest.Members), PayloadSHA256: manifest.PayloadSHA256, MembersSHA256: manifest.MembersSHA256}
	bytes, err := canonicalJSON(descriptor)
	if err != nil {
		return err
	}
	if sha256Hex(bytes) != manifest.DescriptorSHA256 || manifest.DescriptorSHA256 != hash {
		return fmt.Errorf("semantic segment %s descriptor checksum mismatch", hash)
	}
	return nil
}

func validateRootManifest(manifest RootManifest, databaseID, profileID, generationID string) error {
	input := RootInput{DatabaseID: manifest.DatabaseID, ProfileID: manifest.ProfileID, GenerationID: manifest.GenerationID,
		SnapshotRevision: manifest.SnapshotRevision, PurgeEpoch: manifest.PurgeEpoch, Segments: manifest.Segments}
	if manifest.SchemaVersion != SchemaVersion || manifest.DatabaseID != databaseID || manifest.ProfileID != profileID || manifest.GenerationID != generationID {
		return fmt.Errorf("semantic root %s identity mismatch", generationID)
	}
	if err := validateRootInput(input); err != nil {
		return err
	}
	descriptor := rootDescriptor{SchemaVersion: manifest.SchemaVersion, DatabaseID: manifest.DatabaseID, ProfileID: manifest.ProfileID,
		GenerationID: manifest.GenerationID, SnapshotRevision: manifest.SnapshotRevision, PurgeEpoch: manifest.PurgeEpoch,
		Segments: manifest.Segments}
	bytes, err := canonicalJSON(descriptor)
	if err != nil {
		return err
	}
	if sha256Hex(bytes) != manifest.DescriptorSHA256 {
		return fmt.Errorf("semantic root %s descriptor checksum mismatch", generationID)
	}
	return nil
}

func writePayload(path string, write func(io.Writer) error) (string, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create semantic payload: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	counted := &countingWriter{writer: io.MultiWriter(file, hash)}
	err = write(counted)
	if err != nil {
		return "", fmt.Errorf("write semantic payload: %w", err)
	}
	if counted.written == 0 {
		return "", fmt.Errorf("semantic payload is empty")
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync semantic payload: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close semantic payload: %w", err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func writeFileSync(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create semantic manifest: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(contents); err != nil {
		return fmt.Errorf("write semantic manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync semantic manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close semantic manifest: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open semantic directory for sync: %w", err)
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync semantic directory: %w", err)
	}
	return nil
}

func decodeStrictJSON(bytes []byte, destination any) error {
	decoder := json.NewDecoder(&byteReader{bytes: bytes})
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

type countingWriter struct {
	writer  io.Writer
	written int64
}

func (w *countingWriter) Write(contents []byte) (int, error) {
	count, err := w.writer.Write(contents)
	w.written += int64(count)
	return count, err
}

type byteReader struct{ bytes []byte }

func (r *byteReader) Read(destination []byte) (int, error) {
	if len(r.bytes) == 0 {
		return 0, io.EOF
	}
	count := copy(destination, r.bytes)
	r.bytes = r.bytes[count:]
	return count, nil
}
