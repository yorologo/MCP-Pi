package policy

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

const PrivilegeApprovalTTLSeconds = 300

var privilegePolicies = map[string]bool{
	"never":             true,
	"ask_always":        true,
	"ask_once_per_boot": true,
	"always_allow":      true,
}

var privilegeRequestModes = map[string]bool{
	"standard": true,
	"required": true,
}

type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

func validationError(code, message string) error {
	return &ValidationError{Code: code, Message: message}
}

func ValidationErrorCode(err error) string {
	if e, ok := err.(*ValidationError); ok {
		return e.Code
	}
	return ""
}

func NormalizePrivilegePolicy(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		normalized = "never"
	}
	if !privilegePolicies[normalized] {
		return "", validationError(
			"INVALID_PRIVILEGE_POLICY",
			fmt.Sprintf("Unsupported privilege policy: '%s'", value),
		)
	}
	return normalized, nil
}

func NormalizePrivilegeRequest(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		normalized = "standard"
	}
	if !privilegeRequestModes[normalized] {
		return "", validationError(
			"INVALID_PRIVILEGE_REQUEST",
			fmt.Sprintf("Unsupported privilege request: '%s'", value),
		)
	}
	return normalized, nil
}

func ValidateRelativePath(relativePath string) (string, error) {
	cleaned := strings.TrimSpace(relativePath)
	if cleaned == "" {
		return "", validationError("INVALID_PATH", "Path cannot be empty")
	}
	if strings.ContainsRune(cleaned, 0) {
		return "", validationError("INVALID_PATH", "Path contains NUL byte")
	}
	if len(cleaned) >= 2 &&
		((cleaned[0] >= 'a' && cleaned[0] <= 'z') || (cleaned[0] >= 'A' && cleaned[0] <= 'Z')) &&
		cleaned[1] == ':' {
		return "", validationError("INVALID_PATH", "Absolute drive paths are not allowed")
	}
	if strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "\\") || strings.HasPrefix(cleaned, "~") {
		return "", validationError("INVALID_PATH", "Absolute paths are not allowed")
	}

	unified := strings.ReplaceAll(cleaned, "\\", "/")
	parts := strings.Split(unified, "/")
	for i, part := range parts {
		if part == ".." {
			return "", validationError("INVALID_PATH", "Parent directory traversal ('..') is not allowed")
		}
		if part == "" && len(parts) > 1 && i != len(parts)-1 {
			return "", validationError("INVALID_PATH", "Empty path segment is not allowed")
		}
	}

	normalized := path.Clean(unified)
	if normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", validationError("INVALID_PATH", "Path attempts to escape workspace")
	}
	return normalized, nil
}

func ValidateWriteRelativePath(relativePath string) (string, error) {
	normalized, err := ValidateRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	if normalized == "." || normalized == "" {
		return "", validationError("INVALID_PATH", "Target path cannot be the root directory")
	}
	return normalized, nil
}

func ValidateCanonicalPath(canonicalPath, allowedRoot, platformName string) error {
	root := normalizeRemotePath(allowedRoot, platformName)
	canonical := normalizeRemotePath(canonicalPath, platformName)
	if !pathWithinRoot(canonical, root, platformName) {
		return validationError(
			"PATH_OUTSIDE_ALLOWED_ROOT",
			fmt.Sprintf("Path '%s' resolves outside allowed root '%s'", canonicalPath, allowedRoot),
		)
	}
	return nil
}

func normalizeRemotePath(value, platformName string) string {
	windows := strings.EqualFold(strings.TrimSpace(platformName), "windows")
	if windows {
		value = strings.ReplaceAll(value, "\\", "/")
		value = path.Clean(value)
		return strings.ToLower(value)
	}
	return path.Clean(value)
}

func pathWithinRoot(canonical, root, platformName string) bool {
	windows := strings.EqualFold(strings.TrimSpace(platformName), "windows")

	if windows {
		canonDrive, canonRest := splitWindowsDrive(canonical)
		rootDrive, rootRest := splitWindowsDrive(root)
		if canonDrive != rootDrive {
			return false
		}
		if strings.HasPrefix(canonRest, "/") != strings.HasPrefix(rootRest, "/") {
			return false
		}
		canonical = canonRest
		root = rootRest
	} else if strings.HasPrefix(canonical, "/") != strings.HasPrefix(root, "/") {
		return false
	}

	if canonical == root {
		return true
	}
	if root == "/" {
		return strings.HasPrefix(canonical, "/")
	}
	root = strings.TrimSuffix(root, "/")
	return strings.HasPrefix(canonical, root+"/")
}

func splitWindowsDrive(value string) (string, string) {
	if len(value) >= 2 && value[1] == ':' {
		return value[:2], value[2:]
	}
	if strings.HasPrefix(value, "//") {
		trimmed := strings.TrimPrefix(value, "//")
		parts := strings.SplitN(trimmed, "/", 3)
		if len(parts) >= 2 {
			drive := "//" + parts[0] + "/" + parts[1]
			rest := "/"
			if len(parts) == 3 {
				rest += parts[2]
			}
			return drive, rest
		}
	}
	return "", value
}

type CapabilityState struct {
	Read  *bool
	Write *bool
}

func CheckCapability(project CapabilityState, capability string) error {
	switch capability {
	case "read":
		enabled := true
		if project.Read != nil {
			enabled = *project.Read
		}
		if !enabled {
			return validationError("TOOL_NOT_ALLOWED", "Read capability is disabled for this project")
		}
		return nil
	case "write":
		enabled := false
		if project.Write != nil {
			enabled = *project.Write
		}
		if !enabled {
			return validationError("WRITE_NOT_ALLOWED", "Write capability is disabled for this project")
		}
		return nil
	default:
		return validationError("TOOL_NOT_ALLOWED", fmt.Sprintf("Unknown capability: %s", capability))
	}
}

func ValidateContentUTF8(content string) ([]byte, error) {
	if strings.ContainsRune(content, 0) {
		return nil, validationError("INVALID_ENCODING", "Content contains NUL byte")
	}
	if !utf8.ValidString(content) {
		return nil, validationError("INVALID_ENCODING", "Content is not valid UTF-8")
	}
	return []byte(content), nil
}

func ValidateWriteSize(content []byte, maxBytes int) error {
	if len(content) > maxBytes {
		return validationError(
			"FILE_TOO_LARGE",
			fmt.Sprintf("Content size (%d bytes) exceeds allowed limit of %d bytes", len(content), maxBytes),
		)
	}
	return nil
}
