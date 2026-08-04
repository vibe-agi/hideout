package migration

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestResumeWriterAuthenticatesCheckpointTruncatesTailAndContinues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.partial")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	passphrase := []byte("resume passphrase")
	metadata := []byte(`{"schema":"fixture.profile/v1","value":"safe"}`)
	first := []byte("disk")
	second := []byte("data")
	writer, err := NewWriter(file, WriterOptions{
		BundleID: "migb_resumejourney1", CreatedAt: "2026-08-03T00:00:00Z",
		KDF: unitKDFParameters(), Limits: DefaultLimits(),
		Random: bytes.NewReader(deterministicRandomFixture(4096)), Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(RecordInput{
		Type: RecordMetadata, ComponentID: "component_profile0001", Plaintext: metadata,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writer.AppendCheckpoint(CheckpointInput{
		OperationID:         "migop_resumejourney1",
		CompletedComponents: []OpaqueID{"component_profile0001"},
		CurrentComponent:    "component_profile0001",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(RecordInput{
		Type: RecordRawChunk, ComponentID: "component_disk0001",
		LogicalOffset: 0, Plaintext: first,
	}); err != nil {
		t.Fatal(err)
	}
	_, checkpointReceipt, err := writer.AppendCheckpoint(CheckpointInput{
		OperationID:         "migop_resumejourney1",
		CompletedComponents: []OpaqueID{"component_profile0001"},
		CurrentComponent:    "component_disk0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	checkpointEnd, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(RecordInput{
		Type: RecordRawChunk, ComponentID: "component_disk0001",
		Ordinal: 2, LogicalOffset: uint64(len(first)), Plaintext: second,
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() <= checkpointEnd {
		t.Fatalf("tail was not written: checkpoint=%d size=%d", checkpointEnd, before.Size())
	}

	resumed, err := ResumeWriter(file, before.Size(), ResumeOptions{
		BundleID: "migb_resumejourney1", OperationID: "migop_resumejourney1",
		CreatedAt: "2026-08-03T00:00:00Z", Passphrase: passphrase,
		Random: bytes.NewReader(deterministicRandomFixture(4097)[1:]), Limits: DefaultLimits(),
		ExpectedCheckpointDigest: checkpointReceipt.FrameDigest,
		Components: []ResumeComponentSpec{
			{ComponentID: "component_profile0001", Kind: "profile", LogicalBytes: uint64(len(metadata))},
			{ComponentID: "component_disk0001", Kind: "disk", LogicalBytes: uint64(len(first) + len(second))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Checkpoint == nil || resumed.CheckpointFrameDigest != checkpointReceipt.FrameDigest ||
		resumed.CheckpointOffset != uint64(checkpointEnd) || resumed.TruncatedBytes == 0 ||
		len(resumed.Components) != 2 || resumed.Components[1].NextOrdinal != 2 ||
		resumed.Components[1].NextLogicalOffset != uint64(len(first)) {
		t.Fatalf("resume result=%+v", resumed)
	}
	if _, err := resumed.Writer.Append(RecordInput{
		Type: RecordRawChunk, ComponentID: "component_disk0001",
		Ordinal:       resumed.Components[1].NextOrdinal,
		LogicalOffset: resumed.Components[1].NextLogicalOffset, Plaintext: second,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resumed.Writer.AppendCheckpoint(CheckpointInput{
		OperationID:         "migop_resumejourney1",
		CompletedComponents: []OpaqueID{"component_profile0001", "component_disk0001"},
		CurrentComponent:    "component_disk0001",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Writer.Seal([]byte(`{
		"schema":"hideout.migration-manifest/v1",
		"bundleId":"migb_resumejourney1",
		"formatVersion":1
	}`)); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	sealed, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewReader(file, sealed.Size(), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var types []RecordType
	for {
		record, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		types = append(types, record.Header.Type)
		clear(record.Plaintext)
	}
	wantTypes := []RecordType{
		RecordMetadata, RecordCheckpoint, RecordRawChunk, RecordCheckpoint,
		RecordRawChunk, RecordCheckpoint, RecordFinalManifest, RecordCompletion,
	}
	if !bytes.Equal(recordTypesAsBytes(types), recordTypesAsBytes(wantTypes)) {
		t.Fatalf("record types=%v want=%v", types, wantTypes)
	}
}

func TestResumeWriterFailsClosedOnCheckpointDriftWithoutMutation(t *testing.T) {
	path, receipt, _, options := writeResumeCheckpointFixture(t, false)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	options.ExpectedCheckpointDigest = Digest("sha256:" + string(bytes.Repeat([]byte{'f'}, 64)))
	if _, err := ResumeWriter(file, int64(len(before)), options); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("wrong durable checkpoint error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("checkpoint mismatch mutated the partial bundle")
	}

	if _, err := file.WriteAt([]byte{0x80}, int64(receipt.Offset+FrameHeaderSize)); err != nil {
		t.Fatal(err)
	}
	tampered, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	options.ExpectedCheckpointDigest = receipt.FrameDigest
	if _, err := ResumeWriter(file, int64(len(tampered)), options); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("substituted checkpoint error=%v", err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(tampered, unchanged) {
		t.Fatal("failed checkpoint authentication truncated the partial bundle")
	}
}

func TestResumeWriterTruncatesRecordBoundaryTearToDurableCheckpoint(t *testing.T) {
	path, receipt, _, options := writeResumeCheckpointFixture(t, false)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	checkpointInfo, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	torn := []byte("HIDREC01\x01\x00\x00\x00\x00\x00\x00\x00\x00")
	if _, err := file.Write(torn); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeWriter(file, checkpointInfo.Size()+int64(len(torn)), options)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Writer.Close()
	if resumed.CheckpointFrameDigest != receipt.FrameDigest ||
		resumed.TruncatedBytes != uint64(len(torn)) {
		t.Fatalf("torn resume=%+v", resumed)
	}
	after, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != checkpointInfo.Size() {
		t.Fatalf("torn tail size=%d want=%d", after.Size(), checkpointInfo.Size())
	}
}

func TestResumeWriterNeverTruncatesSealedBundle(t *testing.T) {
	path, _, _, options := writeResumeCheckpointFixture(t, true)
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest := sha256.Sum256(before)
	if _, err := ResumeWriter(file, int64(len(before)), options); !errors.Is(err, ErrBundleAlreadySealed) {
		t.Fatalf("sealed resume error=%v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(after) != beforeDigest || !bytes.Equal(before, after) {
		t.Fatal("resume mutated a sealed bundle")
	}
}

func TestDurableExportFormatCrashCuts(t *testing.T) {
	t.Run("bundle header synced", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "header.partial")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		passphrase := []byte("durable header passphrase")
		writer, err := NewWriter(file, WriterOptions{
			BundleID: "migb_headercrashcut1", CreatedAt: "2026-08-04T00:00:00Z",
			KDF: unitKDFParameters(), Limits: DefaultLimits(),
			Random: bytes.NewReader(deterministicRandomFixture(4096)), Passphrase: passphrase,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() <= PrologueSize {
			t.Fatalf("durable header size=%d", info.Size())
		}

		resumed, err := ResumeWriter(file, info.Size(), ResumeOptions{
			BundleID: "migb_headercrashcut1", OperationID: "migop_headercrashcut1",
			CreatedAt: "2026-08-04T00:00:00Z", Passphrase: passphrase,
			Random: bytes.NewReader(deterministicRandomFixture(4097)[1:]),
			Limits: DefaultLimits(),
			Components: []ResumeComponentSpec{{
				ComponentID: "component_profile0001", Kind: "profile", LogicalBytes: 7,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if resumed.Checkpoint != nil || resumed.CheckpointOffset != uint64(info.Size()) ||
			resumed.TruncatedBytes != 0 || len(resumed.Components) != 0 {
			t.Fatalf("header-only resume=%+v", resumed)
		}
		if _, err := resumed.Writer.Append(RecordInput{
			Type: RecordMetadata, ComponentID: "component_profile0001",
			Plaintext: []byte("profile"),
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := resumed.Writer.AppendCheckpoint(CheckpointInput{
			OperationID:         "migop_headercrashcut1",
			CompletedComponents: []OpaqueID{"component_profile0001"},
			CurrentComponent:    "component_profile0001",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := resumed.Writer.Seal([]byte(`{
			"schema":"hideout.migration-manifest/v1",
			"bundleId":"migb_headercrashcut1",
			"formatVersion":1
		}`)); err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := resumed.Writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("manifest written", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "manifest.partial")
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		passphrase := []byte("durable manifest passphrase")
		crashErr := errors.New("injected cut after final manifest")
		output := &crashCutWriteFailer{output: file, failAt: 9, failure: crashErr}
		writer, err := NewWriter(output, WriterOptions{
			BundleID: "migb_manifestcrash1", CreatedAt: "2026-08-04T00:00:00Z",
			KDF: unitKDFParameters(), Limits: DefaultLimits(),
			Random: bytes.NewReader(deterministicRandomFixture(4096)), Passphrase: passphrase,
		})
		if err != nil {
			t.Fatal(err)
		}
		payload := []byte("profile")
		if _, err := writer.Append(RecordInput{
			Type: RecordMetadata, ComponentID: "component_profile0001", Plaintext: payload,
		}); err != nil {
			t.Fatal(err)
		}
		_, checkpoint, err := writer.AppendCheckpoint(CheckpointInput{
			OperationID:         "migop_manifestcrash1",
			CompletedComponents: []OpaqueID{"component_profile0001"},
			CurrentComponent:    "component_profile0001",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		checkpointEnd, err := file.Seek(0, io.SeekCurrent)
		if err != nil {
			t.Fatal(err)
		}
		manifest := []byte(`{
			"schema":"hideout.migration-manifest/v1",
			"bundleId":"migb_manifestcrash1",
			"formatVersion":1
		}`)
		if _, err := writer.Seal(manifest); !errors.Is(err, crashErr) {
			t.Fatalf("manifest crash-cut error=%v writes=%d", err, output.calls)
		}
		if output.calls != 9 {
			t.Fatalf("manifest crash-cut writes=%d want=9", output.calls)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		before, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if before.Size() <= checkpointEnd {
			t.Fatalf("manifest tail size=%d checkpoint=%d", before.Size(), checkpointEnd)
		}

		resumed, err := ResumeWriter(file, before.Size(), ResumeOptions{
			BundleID: "migb_manifestcrash1", OperationID: "migop_manifestcrash1",
			CreatedAt: "2026-08-04T00:00:00Z", Passphrase: passphrase,
			Random: bytes.NewReader(deterministicRandomFixture(4097)[1:]),
			Limits: DefaultLimits(), ExpectedCheckpointDigest: checkpoint.FrameDigest,
			Components: []ResumeComponentSpec{{
				ComponentID: "component_profile0001", Kind: "profile",
				LogicalBytes: uint64(len(payload)),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if resumed.Checkpoint == nil || resumed.CheckpointOffset != uint64(checkpointEnd) ||
			resumed.TruncatedBytes != uint64(before.Size()-checkpointEnd) {
			t.Fatalf("manifest resume=%+v", resumed)
		}
		if _, err := resumed.Writer.Seal(manifest); err != nil {
			t.Fatal(err)
		}
		if err := file.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := resumed.Writer.Close(); err != nil {
			t.Fatal(err)
		}
		sealed, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		reader, err := NewReader(file, sealed.Size(), passphrase)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		var types []RecordType
		for {
			record, readErr := reader.Next()
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			types = append(types, record.Header.Type)
			clear(record.Plaintext)
		}
		want := []RecordType{RecordMetadata, RecordCheckpoint, RecordFinalManifest, RecordCompletion}
		if !bytes.Equal(recordTypesAsBytes(types), recordTypesAsBytes(want)) {
			t.Fatalf("manifest recovery types=%v want=%v", types, want)
		}
	})
}

type crashCutWriteFailer struct {
	output  io.Writer
	failAt  int
	calls   int
	failure error
}

func (writer *crashCutWriteFailer) Write(input []byte) (int, error) {
	writer.calls++
	if writer.calls == writer.failAt {
		return 0, writer.failure
	}
	return writer.output.Write(input)
}

func writeResumeCheckpointFixture(
	t *testing.T,
	seal bool,
) (string, RecordReceipt, []byte, ResumeOptions) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.partial")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	passphrase := []byte("resume fixture passphrase")
	payload := []byte("payload")
	writer, err := NewWriter(file, WriterOptions{
		BundleID: "migb_resumefixture1", CreatedAt: "2026-08-03T00:00:00Z",
		KDF: unitKDFParameters(), Limits: DefaultLimits(),
		Random: bytes.NewReader(deterministicRandomFixture(4096)), Passphrase: passphrase,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(RecordInput{
		Type: RecordMetadata, ComponentID: "component_profile0001", Plaintext: payload,
	}); err != nil {
		t.Fatal(err)
	}
	_, receipt, err := writer.AppendCheckpoint(CheckpointInput{
		OperationID:         "migop_resumefixture1",
		CompletedComponents: []OpaqueID{"component_profile0001"},
		CurrentComponent:    "component_profile0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if seal {
		if _, err := writer.Seal([]byte(`{
			"schema":"hideout.migration-manifest/v1",
			"bundleId":"migb_resumefixture1",
			"formatVersion":1
		}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path, receipt, passphrase, ResumeOptions{
		BundleID: "migb_resumefixture1", OperationID: "migop_resumefixture1",
		CreatedAt: "2026-08-03T00:00:00Z", Passphrase: passphrase,
		Random: bytes.NewReader(deterministicRandomFixture(4096)), Limits: DefaultLimits(),
		ExpectedCheckpointDigest: receipt.FrameDigest,
		Components: []ResumeComponentSpec{{
			ComponentID: "component_profile0001", Kind: "profile",
			LogicalBytes: uint64(len(payload)),
		}},
	}
}

func recordTypesAsBytes(values []RecordType) []byte {
	output := make([]byte, len(values))
	for index, value := range values {
		output[index] = byte(value)
	}
	return output
}
