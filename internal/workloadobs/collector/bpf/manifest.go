package bpf

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/cilium/ebpf"
)

const (
	ArtifactManifestSchema         = "hideout.generated-bpf/v2"
	ObserverSourcePath             = "internal/workloadobs/collector/bpf/programs.c"
	ObserverObjectPath             = "internal/workloadobs/collector/bpf/observer_bpfel.o"
	ObserverGeneratedGoPath        = "internal/workloadobs/collector/bpf/observer_bpfel.go"
	FileObserverSourcePath         = "internal/workloadobs/collector/bpf/file_programs.c"
	FileObserverObjectPath         = "internal/workloadobs/collector/bpf/file_observer_bpfel.o"
	FileObserverGeneratedGoPath    = "internal/workloadobs/collector/bpf/file_observer_bpfel.go"
	NetworkObserverSourcePath      = "internal/workloadobs/collector/bpf/network_programs.c"
	NetworkObserverObjectPath      = "internal/workloadobs/collector/bpf/network_observer_bpfel.o"
	NetworkObserverGeneratedGoPath = "internal/workloadobs/collector/bpf/network_observer_bpfel.go"
	ObserverSourceLicense          = "Apache-2.0 OR GPL-2.0-only"
	ObserverProgramLicense         = "GPL"
	ObserverTarget                 = "bpfel"
	ObserverBPF2GoVersion          = "v0.22.0"
	ObserverCompilerVersion        = "19.1.7"
)

var (
	ErrArtifactManifest  = errors.New("workload observer artifact manifest is invalid")
	ErrArtifactIntegrity = errors.New("workload observer embedded artifact digest mismatch")

	artifactSHA256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

	//go:embed observer.generated.json
	embeddedArtifactManifest []byte

	//go:embed file_observer.generated.json
	embeddedFileArtifactManifest []byte

	//go:embed network_observer.generated.json
	embeddedNetworkArtifactManifest []byte
)

type ArtifactManifest struct {
	Schema               string `json:"schema"`
	Source               string `json:"source"`
	SourceSHA256         string `json:"sourceSHA256"`
	Object               string `json:"object"`
	ObjectSHA256         string `json:"objectSHA256"`
	GeneratedGo          string `json:"generatedGo"`
	GeneratedGoSHA256    string `json:"generatedGoSHA256"`
	Target               string `json:"target"`
	Compiler             string `json:"compiler"`
	CompilerVersion      string `json:"compilerVersion"`
	BPF2GoVersion        string `json:"bpf2goVersion"`
	GoVersion            string `json:"goVersion"`
	License              string `json:"license"`
	KernelProgramLicense string `json:"kernelProgramLicense"`
}

func EmbeddedArtifactManifest() (ArtifactManifest, error) {
	return decodeArtifactManifestFor(
		embeddedArtifactManifest,
		ObserverSourcePath,
		ObserverObjectPath,
		ObserverGeneratedGoPath,
	)
}

func EmbeddedFileArtifactManifest() (ArtifactManifest, error) {
	return decodeArtifactManifestFor(
		embeddedFileArtifactManifest,
		FileObserverSourcePath,
		FileObserverObjectPath,
		FileObserverGeneratedGoPath,
	)
}

func EmbeddedNetworkArtifactManifest() (ArtifactManifest, error) {
	return decodeArtifactManifestFor(
		embeddedNetworkArtifactManifest,
		NetworkObserverSourcePath,
		NetworkObserverObjectPath,
		NetworkObserverGeneratedGoPath,
	)
}

func VerifyEmbeddedArtifacts() (ArtifactManifest, error) {
	manifest, err := EmbeddedArtifactManifest()
	if err != nil {
		return ArtifactManifest{}, err
	}
	if err := verifyArtifactObject(manifest, _ObserverBytes); err != nil {
		return ArtifactManifest{}, err
	}
	return manifest, nil
}

func VerifyEmbeddedFileArtifacts() (ArtifactManifest, error) {
	manifest, err := EmbeddedFileArtifactManifest()
	if err != nil {
		return ArtifactManifest{}, err
	}
	if err := verifyArtifactObjectFor(
		manifest,
		_FileObserverBytes,
		FileObserverSourcePath,
		FileObserverObjectPath,
		FileObserverGeneratedGoPath,
	); err != nil {
		return ArtifactManifest{}, err
	}
	return manifest, nil
}

func VerifyEmbeddedNetworkArtifacts() (ArtifactManifest, error) {
	manifest, err := EmbeddedNetworkArtifactManifest()
	if err != nil {
		return ArtifactManifest{}, err
	}
	if err := verifyArtifactObjectFor(
		manifest,
		_NetworkObserverBytes,
		NetworkObserverSourcePath,
		NetworkObserverObjectPath,
		NetworkObserverGeneratedGoPath,
	); err != nil {
		return ArtifactManifest{}, err
	}
	return manifest, nil
}

// LoadEmbeddedSpec verifies the package-owned digest before allowing the
// kernel loader to parse the embedded ELF. Callers must still probe and attach
// each hook independently; a valid artifact is provenance, not coverage.
func LoadEmbeddedSpec() (*ebpf.CollectionSpec, ArtifactManifest, error) {
	manifest, err := VerifyEmbeddedArtifacts()
	if err != nil {
		return nil, ArtifactManifest{}, err
	}
	spec, err := loadObserver()
	if err != nil {
		return nil, ArtifactManifest{}, err
	}
	return spec, manifest, nil
}

func LoadEmbeddedFileSpec() (*ebpf.CollectionSpec, ArtifactManifest, error) {
	manifest, err := VerifyEmbeddedFileArtifacts()
	if err != nil {
		return nil, ArtifactManifest{}, err
	}
	spec, err := loadFileObserver()
	if err != nil {
		return nil, ArtifactManifest{}, err
	}
	return spec, manifest, nil
}

func LoadEmbeddedNetworkSpec() (*ebpf.CollectionSpec, ArtifactManifest, error) {
	manifest, err := VerifyEmbeddedNetworkArtifacts()
	if err != nil {
		return nil, ArtifactManifest{}, err
	}
	spec, err := loadNetworkObserver()
	if err != nil {
		return nil, ArtifactManifest{}, err
	}
	return spec, manifest, nil
}

func EmbeddedObjectDigest() (string, error) {
	manifest, err := VerifyEmbeddedArtifacts()
	if err != nil {
		return "", err
	}
	return "sha256:" + manifest.ObjectSHA256, nil
}

func EmbeddedFileObjectDigest() (string, error) {
	manifest, err := VerifyEmbeddedFileArtifacts()
	if err != nil {
		return "", err
	}
	return "sha256:" + manifest.ObjectSHA256, nil
}

func EmbeddedNetworkObjectDigest() (string, error) {
	manifest, err := VerifyEmbeddedNetworkArtifacts()
	if err != nil {
		return "", err
	}
	return "sha256:" + manifest.ObjectSHA256, nil
}

func decodeArtifactManifest(data []byte) (ArtifactManifest, error) {
	return decodeArtifactManifestFor(
		data,
		ObserverSourcePath,
		ObserverObjectPath,
		ObserverGeneratedGoPath,
	)
}

func decodeArtifactManifestFor(
	data []byte,
	sourcePath, objectPath, generatedGoPath string,
) (ArtifactManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest ArtifactManifest
	if err := decoder.Decode(&manifest); err != nil {
		return ArtifactManifest{}, fmt.Errorf("%w: decode: %v", ErrArtifactManifest, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ArtifactManifest{}, fmt.Errorf("%w: trailing JSON", ErrArtifactManifest)
	}
	if err := manifest.validateFor(sourcePath, objectPath, generatedGoPath); err != nil {
		return ArtifactManifest{}, err
	}
	return manifest, nil
}

func (manifest ArtifactManifest) Validate() error {
	return manifest.validateFor(
		ObserverSourcePath,
		ObserverObjectPath,
		ObserverGeneratedGoPath,
	)
}

func (manifest ArtifactManifest) validateFor(
	sourcePath, objectPath, generatedGoPath string,
) error {
	if manifest.Schema != ArtifactManifestSchema ||
		manifest.Source != sourcePath ||
		manifest.Object != objectPath ||
		manifest.GeneratedGo != generatedGoPath ||
		manifest.Target != ObserverTarget ||
		manifest.Compiler != "clang" ||
		manifest.CompilerVersion != ObserverCompilerVersion ||
		manifest.BPF2GoVersion != ObserverBPF2GoVersion ||
		manifest.License != ObserverSourceLicense ||
		manifest.KernelProgramLicense != ObserverProgramLicense ||
		len(manifest.GoVersion) == 0 || len(manifest.GoVersion) > 64 {
		return ErrArtifactManifest
	}
	for _, digest := range []string{
		manifest.SourceSHA256,
		manifest.ObjectSHA256,
		manifest.GeneratedGoSHA256,
	} {
		if !artifactSHA256Pattern.MatchString(digest) {
			return ErrArtifactManifest
		}
	}
	return nil
}

func verifyArtifactObject(manifest ArtifactManifest, object []byte) error {
	return verifyArtifactObjectFor(
		manifest,
		object,
		ObserverSourcePath,
		ObserverObjectPath,
		ObserverGeneratedGoPath,
	)
}

func verifyArtifactObjectFor(
	manifest ArtifactManifest,
	object []byte,
	sourcePath, objectPath, generatedGoPath string,
) error {
	if err := manifest.validateFor(sourcePath, objectPath, generatedGoPath); err != nil {
		return err
	}
	sum := sha256.Sum256(object)
	if hex.EncodeToString(sum[:]) != manifest.ObjectSHA256 {
		return ErrArtifactIntegrity
	}
	return nil
}
