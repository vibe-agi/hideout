package workspaceattach

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type RootRelation string

const (
	RootSame     RootRelation = "same"
	RootNested   RootRelation = "nested"
	RootDisjoint RootRelation = "disjoint"
)

type RootNesting string

const (
	RootNestingNone            RootNesting = "none"
	RootFirstContainsSecond    RootNesting = "first-contains-second"
	RootFirstContainedBySecond RootNesting = "first-contained-by-second"
	RelationPositionPeer       string      = "peer"
	RelationPositionAncestor   string      = "ancestor"
	RelationPositionDescendant string      = "descendant"
)

// RootRelationResult describes overlap only. It never authorizes provider
// reuse, widens either captured root, or changes attachment admission.
type RootRelationResult struct {
	Relation RootRelation
	Nesting  RootNesting
}

func ClassifyRoots(firstPath, secondPath string) (RootRelationResult, error) {
	firstCanonical, firstIdentity, err := CaptureRootIdentity(firstPath)
	if err != nil {
		return RootRelationResult{}, err
	}
	secondCanonical, secondIdentity, err := CaptureRootIdentity(secondPath)
	if err != nil {
		return RootRelationResult{}, err
	}
	return ClassifyCapturedRoots(firstCanonical, firstIdentity, secondCanonical, secondIdentity)
}

func ClassifyCapturedRoots(firstCanonical string, firstIdentity RootFileIdentity, secondCanonical string, secondIdentity RootFileIdentity) (RootRelationResult, error) {
	if !canonicalRootInput(firstCanonical) || !canonicalRootInput(secondCanonical) {
		return RootRelationResult{}, errors.New("root relation requires canonical absolute paths")
	}
	if err := firstIdentity.Validate(); err != nil {
		return RootRelationResult{}, err
	}
	if err := secondIdentity.Validate(); err != nil {
		return RootRelationResult{}, err
	}
	if firstIdentity == secondIdentity {
		return RootRelationResult{Relation: RootSame, Nesting: RootNestingNone}, nil
	}
	if pathContains(firstCanonical, secondCanonical) {
		return RootRelationResult{Relation: RootNested, Nesting: RootFirstContainsSecond}, nil
	}
	if pathContains(secondCanonical, firstCanonical) {
		return RootRelationResult{Relation: RootNested, Nesting: RootFirstContainedBySecond}, nil
	}
	return RootRelationResult{Relation: RootDisjoint, Nesting: RootNestingNone}, nil
}

type RootRelationNotice struct {
	Relation         RootRelation `json:"relation"`
	SelectedPosition string       `json:"selectedPosition"`
	WorkspaceID      string       `json:"workspaceId"`
	OtherWorkspaceID string       `json:"otherWorkspaceId"`
}

// BuildRootRelationNotice produces redacted, non-authoritative overlap facts.
// The selected attachment remains the sole authority even for same roots.
func BuildRootRelationNotice(selected, other Attachment) (RootRelationNotice, error) {
	if err := selected.Validate(); err != nil {
		return RootRelationNotice{}, err
	}
	if err := other.Validate(); err != nil {
		return RootRelationNotice{}, err
	}
	relation, err := ClassifyCapturedRoots(
		selected.CanonicalHostRoot, selected.RootFileIdentity,
		other.CanonicalHostRoot, other.RootFileIdentity,
	)
	if err != nil {
		return RootRelationNotice{}, err
	}
	position := RelationPositionPeer
	if relation.Nesting == RootFirstContainsSecond {
		position = RelationPositionAncestor
	} else if relation.Nesting == RootFirstContainedBySecond {
		position = RelationPositionDescendant
	}
	return RootRelationNotice{
		Relation: relation.Relation, SelectedPosition: position,
		WorkspaceID: selected.WorkspaceID, OtherWorkspaceID: other.WorkspaceID,
	}, nil
}

func canonicalRootInput(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
