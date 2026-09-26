package policy

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type validationExpectation struct {
	OK      bool   `json:"ok"`
	Value   any    `json:"value"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type validationFixture struct {
	Schema          int `json:"schema"`
	PrivilegePolicy []struct {
		Input  *string               `json:"input"`
		Result validationExpectation `json:"result"`
	} `json:"privilege_policy"`
	PrivilegeRequest []struct {
		Input  *string               `json:"input"`
		Result validationExpectation `json:"result"`
	} `json:"privilege_request"`
	RelativePaths []struct {
		Input  string                `json:"input"`
		Result validationExpectation `json:"result"`
	} `json:"relative_paths"`
	WritePaths []struct {
		Input  string                `json:"input"`
		Result validationExpectation `json:"result"`
	} `json:"write_paths"`
	CanonicalPaths []struct {
		Canonical string                `json:"canonical"`
		Root      string                `json:"root"`
		Platform  *string               `json:"platform"`
		Result    validationExpectation `json:"result"`
	} `json:"canonical_paths"`
	Capabilities []struct {
		Project    map[string]bool       `json:"project"`
		Capability string                `json:"capability"`
		Result     validationExpectation `json:"result"`
	} `json:"capabilities"`
	ContentUTF8 []struct {
		Input  string                `json:"input"`
		Result validationExpectation `json:"result"`
	} `json:"content_utf8"`
	WriteSizes []struct {
		Hex    string                `json:"hex"`
		Max    int                   `json:"max"`
		Result validationExpectation `json:"result"`
	} `json:"write_sizes"`
	Tasks []struct {
		Name    string                `json:"name"`
		Project map[string]any        `json:"project"`
		Task    string                `json:"task"`
		Result  validationExpectation `json:"result"`
	} `json:"tasks"`
}

func loadValidationFixture(t *testing.T) validationFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "contracts", "policy_validation_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture validationFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != 1 {
		t.Fatalf("fixture schema=%d want=1", fixture.Schema)
	}
	return fixture
}

func inputOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func checkValidationResult(t *testing.T, gotValue any, gotErr error, want validationExpectation) {
	t.Helper()
	if want.OK {
		if gotErr != nil {
			t.Fatalf("unexpected error: %v (%s)", gotErr, ValidationErrorCode(gotErr))
		}
		if want.Value != nil && !reflect.DeepEqual(normalizeValidationJSON(t, gotValue), normalizeValidationJSON(t, want.Value)) {
			t.Fatalf("value=%#v want=%#v", gotValue, want.Value)
		}
		return
	}
	if gotErr == nil {
		t.Fatalf("expected %s: %s", want.Code, want.Message)
	}
	if code := ValidationErrorCode(gotErr); code != want.Code {
		t.Fatalf("code=%q want=%q err=%v", code, want.Code, gotErr)
	}
	if gotErr.Error() != want.Message {
		t.Fatalf("message=%q want=%q", gotErr.Error(), want.Message)
	}
}

func normalizeValidationJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		t.Fatal(err)
	}
	return normalized
}

func TestPrivilegeNormalizationMatchesPython(t *testing.T) {
	fixture := loadValidationFixture(t)
	for _, tc := range fixture.PrivilegePolicy {
		value, err := NormalizePrivilegePolicy(inputOrEmpty(tc.Input))
		checkValidationResult(t, value, err, tc.Result)
	}
	for _, tc := range fixture.PrivilegeRequest {
		value, err := NormalizePrivilegeRequest(inputOrEmpty(tc.Input))
		checkValidationResult(t, value, err, tc.Result)
	}
}

func TestRelativePathValidationMatchesPython(t *testing.T) {
	fixture := loadValidationFixture(t)
	for _, tc := range fixture.RelativePaths {
		tc := tc
		t.Run(strings.ReplaceAll(tc.Input, "/", "_"), func(t *testing.T) {
			value, err := ValidateRelativePath(tc.Input)
			checkValidationResult(t, value, err, tc.Result)
		})
	}
}

func TestWritePathValidationMatchesPython(t *testing.T) {
	fixture := loadValidationFixture(t)
	for _, tc := range fixture.WritePaths {
		value, err := ValidateWriteRelativePath(tc.Input)
		checkValidationResult(t, value, err, tc.Result)
	}
}

func TestCanonicalPathValidationMatchesPython(t *testing.T) {
	fixture := loadValidationFixture(t)
	for _, tc := range fixture.CanonicalPaths {
		platform := ""
		if tc.Platform != nil {
			platform = *tc.Platform
		}
		err := ValidateCanonicalPath(tc.Canonical, tc.Root, platform)
		checkValidationResult(t, nil, err, tc.Result)
	}
}

func TestCapabilityValidationMatchesPython(t *testing.T) {
	fixture := loadValidationFixture(t)
	for _, tc := range fixture.Capabilities {
		var state CapabilityState
		if value, ok := tc.Project["read"]; ok {
			value := value
			state.Read = &value
		}
		if value, ok := tc.Project["write"]; ok {
			value := value
			state.Write = &value
		}
		err := CheckCapability(state, tc.Capability)
		checkValidationResult(t, nil, err, tc.Result)
	}
}

func TestContentAndSizeValidationMatchesPython(t *testing.T) {
	fixture := loadValidationFixture(t)
	for _, tc := range fixture.ContentUTF8 {
		value, err := ValidateContentUTF8(tc.Input)
		var got any
		if err == nil {
			got = map[string]any{"utf8_len": len(value), "hex": hex.EncodeToString(value)}
		}
		checkValidationResult(t, got, err, tc.Result)
	}

	for _, tc := range fixture.WriteSizes {
		data, err := hex.DecodeString(tc.Hex)
		if err != nil {
			t.Fatal(err)
		}
		err = ValidateWriteSize(data, tc.Max)
		checkValidationResult(t, nil, err, tc.Result)
	}
}

func TestTaskValidationMatchesPython(t *testing.T) {
	fixture := loadValidationFixture(t)
	for _, tc := range fixture.Tasks {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			value, err := ValidateTask(tc.Project, tc.Task)
			checkValidationResult(t, value, err, tc.Result)
		})
	}
}

func TestInvalidUTF8FailsClosed(t *testing.T) {
	_, err := ValidateContentUTF8(string([]byte{0xff}))
	if ValidationErrorCode(err) != "INVALID_ENCODING" {
		t.Fatalf("err=%v code=%q", err, ValidationErrorCode(err))
	}
}
