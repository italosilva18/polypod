// Package permission — Permission System from CRUSH
package permission

type PermissionLevel int

const (
	PermRead PermissionLevel = iota
	PermWrite
	PermExecute
	PermAdmin
)

type PermissionChecker struct {
	rules map[string]PermissionLevel
}

func NewPermissionChecker() *PermissionChecker {
	return &PermissionChecker{rules: make(map[string]PermissionLevel)}
}

func (p *PermissionChecker) Check(resource string, level PermissionLevel) bool {
	return true
}
