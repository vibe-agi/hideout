package workspacepath

import (
	"errors"
	"path"
	"strings"
)

const (
	LogicalRoot  = "/workspace"
	PhysicalBase = "/hideout/workspaces"
)

var (
	ErrIdentityInvalid   = errors.New("guest workspace identity is invalid")
	ErrPathInvalid       = errors.New("guest workspace path is invalid")
	ErrOutsideAttachment = errors.New("guest workspace path is outside the attachment")
)

type Alias string

const (
	LogicalAlias  Alias = "logical"
	PhysicalAlias Alias = "physical"
)

type Identity struct {
	WorkspaceID  string
	RelativePath string
	LogicalPath  string
	PhysicalPath string
	SourceAlias  Alias
}

type Binding struct {
	WorkspaceID  string
	LogicalRoot  string
	PhysicalRoot string
}

func ValidWorkspaceID(value string) bool {
	return strings.HasPrefix(value, "wrk_") && len(value) == 4+64 &&
		strings.Trim(strings.TrimPrefix(value, "wrk_"), "0123456789abcdef") == ""
}

func NewBinding(workspaceID, logicalRoot, physicalRoot string) (Binding, error) {
	if logicalRoot != LogicalRoot {
		return Binding{}, ErrIdentityInvalid
	}
	resolved, err := Resolve(workspaceID, physicalRoot)
	if err != nil || resolved.RelativePath != "." || resolved.SourceAlias != PhysicalAlias ||
		resolved.PhysicalPath != physicalRoot {
		return Binding{}, ErrIdentityInvalid
	}
	return Binding{
		WorkspaceID: workspaceID, LogicalRoot: logicalRoot, PhysicalRoot: physicalRoot,
	}, nil
}

func BindingFromPhysicalRoot(logicalRoot, physicalRoot string) (Binding, error) {
	clean := path.Clean(physicalRoot)
	if clean != physicalRoot || path.Dir(clean) != PhysicalBase {
		return Binding{}, ErrIdentityInvalid
	}
	return NewBinding(path.Base(clean), logicalRoot, clean)
}

func (binding Binding) Validate() error {
	_, err := NewBinding(binding.WorkspaceID, binding.LogicalRoot, binding.PhysicalRoot)
	return err
}

func (binding Binding) Resolve(guestPath string) (Identity, error) {
	if err := binding.Validate(); err != nil {
		return Identity{}, err
	}
	return Resolve(binding.WorkspaceID, guestPath)
}

// Resolve accepts only the logical root or the exact opaque physical root
// derived from workspaceID. It intentionally performs no generic string
// replacement and never accepts a sibling workspace physical root.
func Resolve(workspaceID, guestPath string) (Identity, error) {
	if !ValidWorkspaceID(workspaceID) {
		return Identity{}, ErrIdentityInvalid
	}
	if guestPath == "" || !path.IsAbs(guestPath) || strings.IndexByte(guestPath, 0) >= 0 || containsParentElement(guestPath) {
		return Identity{}, ErrPathInvalid
	}

	physicalRoot := path.Join(PhysicalBase, workspaceID)
	clean := path.Clean(guestPath)
	relative, alias, ok := relativeToAlias(clean, LogicalRoot, physicalRoot)
	if !ok {
		return Identity{}, ErrOutsideAttachment
	}

	logicalPath, physicalPath := LogicalRoot, physicalRoot
	if relative != "." {
		logicalPath = path.Join(LogicalRoot, relative)
		physicalPath = path.Join(physicalRoot, relative)
	}
	return Identity{
		WorkspaceID: workspaceID, RelativePath: relative,
		LogicalPath: logicalPath, PhysicalPath: physicalPath, SourceAlias: alias,
	}, nil
}

func relativeToAlias(candidate, logicalRoot, physicalRoot string) (string, Alias, bool) {
	for _, root := range []struct {
		path  string
		alias Alias
	}{
		{path: logicalRoot, alias: LogicalAlias},
		{path: physicalRoot, alias: PhysicalAlias},
	} {
		if candidate == root.path {
			return ".", root.alias, true
		}
		prefix := root.path + "/"
		if strings.HasPrefix(candidate, prefix) {
			return strings.TrimPrefix(candidate, prefix), root.alias, true
		}
	}
	return "", "", false
}

func containsParentElement(value string) bool {
	for _, element := range strings.Split(value, "/") {
		if element == ".." {
			return true
		}
	}
	return false
}
