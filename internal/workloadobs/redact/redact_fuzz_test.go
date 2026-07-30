package redact

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzRedactorTextAndArgvFailClosed(f *testing.F) {
	const (
		secret = "FUZZ-SECRET-CANARY-045"
		token  = "token_FUZZ_CONTROL_CANARY_045"
	)
	redactor, err := New(Config{
		KnownSecrets:   [][]byte{[]byte(secret)},
		ControlTokens:  []string{token},
		MaxValueBytes:  1024,
		MaxOutputBytes: 4096,
		MaxArguments:   8,
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(redactor.Clear)
	for _, seed := range []string{
		"ordinary text",
		secret,
		"Authorization: Bearer " + token,
		"socks5://fuzz-user:fuzz-password@127.0.0.1:7890",
		"https://example.invalid/?access_token=" + secret,
		strings.Repeat("x", 2048) + secret,
		string([]byte{0xff, 0xfe}),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		first, firstTruncation, firstErr := redactor.Text(input)
		second, secondTruncation, secondErr := redactor.Text(input)
		if !sameErrorClass(firstErr, secondErr) ||
			first != second ||
			strings.Join(firstTruncation, "\x00") !=
				strings.Join(secondTruncation, "\x00") {
			t.Fatalf("text redaction is not deterministic")
		}
		if firstErr == nil {
			assertFuzzRedactionOutput(t, first, secret, token)
		} else if !errors.Is(firstErr, ErrRedactionFailed) {
			t.Fatalf("unexpected text redaction error: %v", firstErr)
		}

		arguments := []string{
			"agent",
			"--token",
			input,
			"https://example.invalid/?password=" + input,
		}
		firstArgv, firstArgvTruncation, firstArgvErr := redactor.Argv(arguments)
		secondArgv, secondArgvTruncation, secondArgvErr := redactor.Argv(arguments)
		if !sameErrorClass(firstArgvErr, secondArgvErr) ||
			strings.Join(firstArgv, "\x00") != strings.Join(secondArgv, "\x00") ||
			strings.Join(firstArgvTruncation, "\x00") !=
				strings.Join(secondArgvTruncation, "\x00") {
			t.Fatalf("argv redaction is not deterministic")
		}
		if firstArgvErr == nil {
			assertFuzzRedactionOutput(
				t,
				strings.Join(firstArgv, "\n"),
				secret,
				token,
			)
		} else if !errors.Is(firstArgvErr, ErrRedactionFailed) {
			t.Fatalf("unexpected argv redaction error: %v", firstArgvErr)
		}
	})
}

func assertFuzzRedactionOutput(
	t *testing.T,
	output string,
	forbidden ...string,
) {
	t.Helper()
	if !utf8.ValidString(output) || len(output) > 4096 {
		t.Fatalf("redaction produced invalid or unbounded output")
	}
	for _, value := range forbidden {
		if strings.Contains(output, value) {
			t.Fatalf("redaction retained protected value %q", value)
		}
	}
}

func sameErrorClass(left, right error) bool {
	return errors.Is(left, ErrRedactionFailed) ==
		errors.Is(right, ErrRedactionFailed)
}
