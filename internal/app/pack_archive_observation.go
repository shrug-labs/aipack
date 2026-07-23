package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"

	"github.com/shrug-labs/aipack/internal/config"
	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/source"
	"github.com/shrug-labs/aipack/internal/util"
)

const archiveObservationVersion = 2

type archiveObservationKey struct {
	Origin             string                         `json:"origin"`
	SubPath            string                         `json:"sub_path,omitempty"`
	ContentPaths       map[domain.PackCategory]string `json:"content_paths,omitempty"`
	SelectionDigest    string                         `json:"selection_digest"`
	InstalledIntegrity string                         `json:"installed_integrity"`
}

type archiveSemanticObservation struct {
	Status            UpdateStatus       `json:"status"`
	Message           string             `json:"message"`
	BundledCandidates *BundledCandidates `json:"bundled_candidates,omitempty"`
}

func (s archiveSemanticObservation) result(name string) PackUpdateResult {
	return PackUpdateResult{
		Name:              name,
		Method:            config.MethodArchive,
		Status:            s.Status,
		Message:           s.Message,
		BundledCandidates: s.BundledCandidates,
	}
}

// archiveObservation is disposable transport cache state. Correctness never
// depends on it: missing, corrupt, or mismatched entries cause a full fetch,
// extraction, and semantic integrity comparison.
type archiveObservation struct {
	Version            int                        `json:"version"`
	Key                archiveObservationKey      `json:"key"`
	ResourceIdentity   string                     `json:"resource_identity,omitempty"`
	Validator          source.ArchiveValidator    `json:"validator,omitempty"`
	ByteHash           string                     `json:"byte_hash,omitempty"`
	CandidateIntegrity string                     `json:"candidate_integrity"`
	Semantic           archiveSemanticObservation `json:"semantic"`
}

func newArchiveObservation(
	key archiveObservationKey,
	fetch source.ArchiveFetchResult,
	candidateIntegrity string,
	result PackUpdateResult,
) archiveObservation {
	return archiveObservation{
		Key:                key,
		ResourceIdentity:   fetch.ResourceIdentity,
		Validator:          fetch.Validator,
		ByteHash:           fetch.ByteHash,
		CandidateIntegrity: candidateIntegrity,
		Semantic: archiveSemanticObservation{
			Status:            result.Status,
			Message:           result.Message,
			BundledCandidates: result.BundledCandidates,
		},
	}
}

func archiveObservationPath(configDir, name string) string {
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(configDir, ".cache", "archive-observations", hex.EncodeToString(sum[:])+".json")
}

func loadArchiveObservation(configDir, name string) (archiveObservation, bool) {
	data, exists, err := util.ReadFileIfExists(archiveObservationPath(configDir, name))
	if err != nil || !exists {
		return archiveObservation{}, false
	}
	var observation archiveObservation
	if json.Unmarshal(data, &observation) != nil || observation.Version != archiveObservationVersion {
		_ = os.Remove(archiveObservationPath(configDir, name))
		return archiveObservation{}, false
	}
	return observation, true
}

func saveArchiveObservation(configDir, name string, observation archiveObservation) error {
	observation.Version = archiveObservationVersion
	data, err := json.MarshalIndent(observation, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, exists, err := util.ReadFileIfExists(archiveObservationPath(configDir, name)); err == nil && exists && bytes.Equal(existing, data) {
		return nil
	}
	return util.WriteFileAtomicWithPerms(archiveObservationPath(configDir, name), data, 0o700, 0o600)
}

func makeArchiveObservationKey(
	origin string,
	subPath string,
	contentPaths map[domain.PackCategory]string,
	selectionDigest string,
	installedIntegrity string,
) archiveObservationKey {
	return archiveObservationKey{
		Origin:             origin,
		SubPath:            subPath,
		ContentPaths:       maps.Clone(contentPaths),
		SelectionDigest:    selectionDigest,
		InstalledIntegrity: installedIntegrity,
	}
}

func archiveObservationMatches(observation archiveObservation, key archiveObservationKey) bool {
	return observation.Key.Origin == key.Origin &&
		observation.Key.SubPath == key.SubPath &&
		observation.Key.SelectionDigest == key.SelectionDigest &&
		observation.Key.InstalledIntegrity == key.InstalledIntegrity &&
		observation.CandidateIntegrity != "" &&
		maps.Equal(observation.Key.ContentPaths, key.ContentPaths)
}

func deleteArchiveObservation(configDir, name string) error {
	err := os.Remove(archiveObservationPath(configDir, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func archiveSelectionDigest(selection domain.BundledSet) (string, error) {
	data, err := json.Marshal(selection)
	if err != nil {
		return "", err
	}
	return util.ContentDigest(data), nil
}

func integrityDigest(manifest IntegrityManifest) (string, error) {
	data, err := json.Marshal(manifest.Files)
	if err != nil {
		return "", err
	}
	return util.ContentDigest(data), nil
}
