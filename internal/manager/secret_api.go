package manager

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/secrets"
)

var errInvalidSecretJSON = errors.New("invalid secret request")

func (api API) serveSecrets(w http.ResponseWriter, r *http.Request) {
	ref, err := secretListRef(r)
	if err != nil {
		writeAPIDetailedError(w, http.StatusBadRequest, APIErrorDetail{
			Code:     "invalid-secret-query",
			Field:    "query",
			Message:  "secret metadata query is invalid",
			Recovery: "use no query or one valid ref query parameter",
		})
		return
	}
	if api.SecretProvider == nil {
		writeSecretAPIError(w, ErrSecretProviderUnavailable)
		return
	}
	references, err := api.SecretProvider.ListSecrets(r.Context(), ref)
	if err != nil {
		writeSecretAPIError(w, err)
		return
	}
	if len(references) > maxKeychainListReferences {
		writeSecretAPIInvalidProviderResult(w)
		return
	}
	references = append([]secrets.Reference(nil), references...)
	secrets.SortReferences(references)
	for _, reference := range references {
		if err := reference.Validate(); err != nil {
			writeSecretAPIInvalidProviderResult(w)
			return
		}
		if ref != "" && reference.Ref != ref {
			writeSecretAPIInvalidProviderResult(w)
			return
		}
	}
	for index := 1; index < len(references); index++ {
		if references[index-1].Ref == references[index].Ref {
			writeSecretAPIInvalidProviderResult(w)
			return
		}
	}
	if ref != "" && len(references) != 1 {
		writeSecretAPIInvalidProviderResult(w)
		return
	}
	if references == nil {
		references = []secrets.Reference{}
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "secrets",
		Data:     references,
		Errors:   []string{},
	})
}

func (api API) serveSecretPlan(w http.ResponseWriter, r *http.Request) {
	draft, err := decodeSecretDraftAPIRequest(w, r)
	if err != nil {
		writeSecretRequestDecodeError(w, err)
		return
	}
	if err := draft.Validate(); err != nil {
		writeSecretRequestDecodeError(w, errInvalidSecretJSON)
		return
	}
	if api.SecretProvider == nil {
		writeSecretAPIError(w, ErrSecretProviderUnavailable)
		return
	}
	plan, err := api.SecretProvider.PlanSecret(r.Context(), draft)
	if err != nil {
		writeSecretAPIError(w, err)
		return
	}
	if err := plan.VerifyDigest(); err != nil ||
		plan.Ref != draft.Ref ||
		plan.Action != draft.Action {
		writeSecretAPIInvalidProviderResult(w)
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "secret/plan",
		Data:     plan,
		Errors:   []string{},
	})
}

func (api API) serveSecretApply(w http.ResponseWriter, r *http.Request) {
	request, err := decodeSecretApplyAPIRequest(w, r)
	if request.Value != nil {
		defer request.Value.Clear()
	}
	if err != nil {
		writeSecretRequestDecodeError(w, err)
		return
	}
	if err := request.Validate(); err != nil {
		writeSecretRequestDecodeError(w, errInvalidSecretJSON)
		return
	}
	if api.SecretProvider == nil {
		writeSecretAPIError(w, ErrSecretProviderUnavailable)
		return
	}
	result, err := api.SecretProvider.ApplySecret(r.Context(), request)
	if err != nil {
		writeSecretAPIError(w, err)
		return
	}
	if err := result.Operation.Validate(); err != nil ||
		result.Reference.Validate() != nil ||
		result.Operation.ID != request.OperationID ||
		result.Operation.PlanDigest != request.PlanDigest ||
		result.Operation.Owner.Kind != "secret" ||
		result.Operation.Owner.ID != request.Ref ||
		result.Reference.Ref != request.Ref {
		writeSecretAPIInvalidProviderResult(w)
		return
	}
	writeAPIJSON(w, http.StatusOK, APIResponse{
		Version:  APIVersion,
		Resource: "secret/apply",
		Data:     result,
		Errors:   []string{},
	})
}

func secretListRef(r *http.Request) (string, error) {
	if r == nil {
		return "", errInvalidSecretJSON
	}
	if len(r.URL.RawQuery) > 512 {
		return "", errInvalidSecretJSON
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", errInvalidSecretJSON
	}
	for key, entries := range values {
		if key != "ref" || len(entries) != 1 {
			return "", errInvalidSecretJSON
		}
	}
	entries, exists := values["ref"]
	if !exists {
		return "", nil
	}
	if len(entries) != 1 || secrets.ValidateRef(entries[0]) != nil {
		return "", errInvalidSecretJSON
	}
	return entries[0], nil
}

func writeSecretRequestDecodeError(w http.ResponseWriter, err error) {
	if err != nil && err.Error() == requestBodyTooLargeMessage {
		writeAPIError(w, http.StatusRequestEntityTooLarge, requestBodyTooLargeMessage)
		return
	}
	writeAPIDetailedError(w, http.StatusBadRequest, APIErrorDetail{
		Code:     "invalid-secret-request",
		Field:    "body",
		Message:  "secret request body is invalid",
		Recovery: "send one strict JSON object using the reviewed secret operation fields",
	})
}

func writeSecretAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	detail := APIErrorDetail{
		Code:     "secret-service-failed",
		Message:  "secret service request failed",
		Recovery: "retry through the running Hideout daemon; inspect daemon status if the failure persists",
	}
	switch {
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
		detail = APIErrorDetail{
			Code:     "secret-operation-not-found",
			Message:  "secret operation was not found",
			Recovery: "create and review a new secret plan",
		}
	case errors.Is(err, ErrInvalidSecretDraft),
		errors.Is(err, ErrInvalidSecretApply),
		errors.Is(err, ErrInvalidSecretPlan),
		errors.Is(err, ErrInvalidOperation):
		status = http.StatusBadRequest
		detail = APIErrorDetail{
			Code:     "invalid-secret-request",
			Message:  "secret request is invalid",
			Recovery: "create and review a fresh secret plan",
		}
	case errors.Is(err, ErrStaleSecretPlan):
		status = http.StatusConflict
		detail = APIErrorDetail{
			Code:     "stale-secret-plan",
			Message:  "secret metadata changed after this plan was reviewed",
			Recovery: "refresh secret status and review a new plan",
		}
	case errors.Is(err, ErrOperationMismatch),
		errors.Is(err, ErrOperationProviderMismatch),
		errors.Is(err, ErrConfigurationMutationConflict),
		errors.Is(err, secrets.ErrSecretGenerationMismatch),
		errors.Is(err, secrets.ErrSecretOperationMismatch):
		status = http.StatusConflict
		detail = APIErrorDetail{
			Code:     "secret-operation-conflict",
			Message:  "secret operation no longer matches the reviewed plan",
			Recovery: "refresh secret status and create a new operation",
		}
	case errors.Is(err, ErrSecretPlanExpired):
		status = http.StatusConflict
		detail = APIErrorDetail{
			Code:     "secret-plan-expired",
			Message:  "secret plan expired before confirmation",
			Recovery: "create and review a fresh secret plan",
		}
	case errors.Is(err, ErrSecretConfirmationRequired):
		status = http.StatusUnprocessableEntity
		detail = APIErrorDetail{
			Code:     "secret-confirmation-required",
			Message:  "secret apply requires explicit confirmation",
			Recovery: "review the plan and confirm the exact operation",
		}
	case errors.Is(err, ErrSecretValueRequired):
		status = http.StatusUnprocessableEntity
		detail = APIErrorDetail{
			Code:     "secret-value-required",
			Message:  "this secret operation requires a value",
			Recovery: "enter the value through a protected prompt or standard input and retry the same operation",
		}
	case errors.Is(err, ErrSecretPlanBlocked):
		status = http.StatusUnprocessableEntity
		detail = APIErrorDetail{
			Code:     "secret-plan-blocked",
			Message:  "secret plan has active blockers",
			Recovery: "resolve the blockers shown by a fresh plan before applying",
		}
	case errors.Is(err, ErrSecretRecoveryRequired):
		status = http.StatusServiceUnavailable
		detail = APIErrorDetail{
			Code:     "secret-recovery-required",
			Message:  "secret operation could not be proved complete",
			Recovery: "retry the exact operation so Hideout can reconcile provider state",
		}
	case errors.Is(err, secrets.ErrSecretLocked):
		status = http.StatusServiceUnavailable
		detail = APIErrorDetail{
			Code:     "secret-provider-locked",
			Message:  "secret provider is locked",
			Recovery: "unlock the login keychain and retry",
		}
	case errors.Is(err, ErrSecretProviderUnavailable),
		errors.Is(err, secrets.ErrProviderUnavailable),
		errors.Is(err, secrets.ErrSecretEnvelopeCorrupt),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		status = http.StatusServiceUnavailable
		detail = APIErrorDetail{
			Code:     "secret-provider-unavailable",
			Message:  "secret provider is unavailable",
			Recovery: "check daemon and login keychain availability, then retry",
		}
	case errors.Is(err, secrets.ErrSecretMissing):
		status = http.StatusNotFound
		detail = APIErrorDetail{
			Code:     "secret-not-found",
			Message:  "secret reference is missing",
			Recovery: "create a set plan for this secret reference",
		}
	}
	writeAPIDetailedError(w, status, detail)
}

func writeSecretAPIInvalidProviderResult(w http.ResponseWriter) {
	writeAPIDetailedError(w, http.StatusServiceUnavailable, APIErrorDetail{
		Code:     "secret-provider-response-invalid",
		Message:  "secret provider returned invalid metadata",
		Recovery: "update Hideout and retry through the running daemon",
	})
}

func decodeSecretDraftAPIRequest(
	w http.ResponseWriter,
	r *http.Request,
) (SecretDraft, error) {
	body, err := readSecretRequestBody(w, r)
	if err != nil {
		return SecretDraft{}, err
	}
	defer clear(body)

	var draft SecretDraft
	parser := secretJSONParser{data: body}
	if !parser.takeObjectStart() {
		return draft, errInvalidSecretJSON
	}
	const (
		draftSchema = 1 << iota
		draftRef
		draftAction
	)
	seen := 0
	if parser.takeObjectEnd() {
		return draft, errInvalidSecretJSON
	}
	for {
		keyBytes, parseErr := parser.string()
		if parseErr != nil {
			return draft, errInvalidSecretJSON
		}
		if !parser.takeColon() {
			clear(keyBytes)
			return draft, errInvalidSecretJSON
		}
		var bit int
		switch {
		case secretASCIIEqual(keyBytes, "schema"):
			bit = draftSchema
		case secretASCIIEqual(keyBytes, "ref"):
			bit = draftRef
		case secretASCIIEqual(keyBytes, "action"):
			bit = draftAction
		}
		clear(keyBytes)
		if bit == 0 {
			return draft, errInvalidSecretJSON
		}
		if seen&bit != 0 {
			return draft, errInvalidSecretJSON
		}
		seen |= bit
		value, parseErr := parser.string()
		if parseErr != nil {
			return draft, errInvalidSecretJSON
		}
		switch bit {
		case draftSchema:
			if !secretASCIIEqual(value, SecretDraftSchema) {
				clear(value)
				return draft, errInvalidSecretJSON
			}
			draft.Schema = SecretDraftSchema
		case draftRef:
			if !validSecretRefBytes(value) {
				clear(value)
				return draft, errInvalidSecretJSON
			}
			draft.Ref = string(value)
		case draftAction:
			action, ok := secretActionBytes(value)
			if !ok {
				clear(value)
				return draft, errInvalidSecretJSON
			}
			draft.Action = action
		}
		clear(value)
		if parser.takeObjectEnd() {
			break
		}
		if !parser.takeComma() {
			return draft, errInvalidSecretJSON
		}
	}
	if !parser.finished() ||
		seen != draftSchema|draftRef|draftAction {
		return draft, errInvalidSecretJSON
	}
	return draft, nil
}

func decodeSecretApplyAPIRequest(
	w http.ResponseWriter,
	r *http.Request,
) (request SecretApplyRequest, err error) {
	body, err := readSecretRequestBody(w, r)
	if err != nil {
		return request, err
	}
	defer clear(body)
	defer func() {
		if err != nil && request.Value != nil {
			request.Value.Clear()
		}
	}()

	parser := secretJSONParser{data: body}
	if !parser.takeObjectStart() {
		return request, errInvalidSecretJSON
	}
	const (
		applySchema = 1 << iota
		applyOperationID
		applyPlanDigest
		applyRef
		applyAction
		applyConfirmed
		applyValue
	)
	const required = applySchema |
		applyOperationID |
		applyPlanDigest |
		applyRef |
		applyAction |
		applyConfirmed
	seen := 0
	if parser.takeObjectEnd() {
		return request, errInvalidSecretJSON
	}
	for {
		keyBytes, parseErr := parser.string()
		if parseErr != nil {
			return request, errInvalidSecretJSON
		}
		if !parser.takeColon() {
			clear(keyBytes)
			return request, errInvalidSecretJSON
		}
		var bit int
		switch {
		case secretASCIIEqual(keyBytes, "schema"):
			bit = applySchema
		case secretASCIIEqual(keyBytes, "operationId"):
			bit = applyOperationID
		case secretASCIIEqual(keyBytes, "planDigest"):
			bit = applyPlanDigest
		case secretASCIIEqual(keyBytes, "ref"):
			bit = applyRef
		case secretASCIIEqual(keyBytes, "action"):
			bit = applyAction
		case secretASCIIEqual(keyBytes, "confirmed"):
			bit = applyConfirmed
		case secretASCIIEqual(keyBytes, "value"):
			bit = applyValue
		}
		clear(keyBytes)
		if bit == 0 {
			return request, errInvalidSecretJSON
		}
		if seen&bit != 0 {
			return request, errInvalidSecretJSON
		}
		seen |= bit

		switch bit {
		case applyConfirmed:
			value, parseErr := parser.boolean()
			if parseErr != nil {
				return request, errInvalidSecretJSON
			}
			request.Confirmed = value
		case applyValue:
			value, parseErr := parser.string()
			if parseErr != nil {
				return request, errInvalidSecretJSON
			}
			request.Value, parseErr = secrets.NewBuffer(value)
			clear(value)
			if parseErr != nil {
				return request, errInvalidSecretJSON
			}
		default:
			value, parseErr := parser.string()
			if parseErr != nil {
				return request, errInvalidSecretJSON
			}
			switch bit {
			case applySchema:
				if secretASCIIEqual(value, SecretApplySchema) {
					request.Schema = SecretApplySchema
				} else {
					parseErr = errInvalidSecretJSON
				}
			case applyOperationID:
				if operationIDPattern.Match(value) {
					request.OperationID = string(value)
				} else {
					parseErr = errInvalidSecretJSON
				}
			case applyPlanDigest:
				if profileDigestPattern.Match(value) {
					request.PlanDigest = string(value)
				} else {
					parseErr = errInvalidSecretJSON
				}
			case applyRef:
				if validSecretRefBytes(value) {
					request.Ref = string(value)
				} else {
					parseErr = errInvalidSecretJSON
				}
			case applyAction:
				var ok bool
				request.Action, ok = secretActionBytes(value)
				if !ok {
					parseErr = errInvalidSecretJSON
				}
			}
			clear(value)
			if parseErr != nil {
				return request, errInvalidSecretJSON
			}
		}
		if parser.takeObjectEnd() {
			break
		}
		if !parser.takeComma() {
			return request, errInvalidSecretJSON
		}
	}
	if !parser.finished() || seen&required != required {
		return request, errInvalidSecretJSON
	}
	return request, nil
}

func readSecretRequestBody(
	w http.ResponseWriter,
	r *http.Request,
) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, errInvalidSecretJSON
	}
	reader := http.MaxBytesReader(w, r.Body, managerRequestBodyLimit(r))
	body, err := io.ReadAll(reader)
	if err != nil {
		clear(body)
		if isMaxBytesError(err) {
			return nil, errors.New(requestBodyTooLargeMessage)
		}
		return nil, errInvalidSecretJSON
	}
	return body, nil
}

type secretJSONParser struct {
	data []byte
	pos  int
}

func (parser *secretJSONParser) takeObjectStart() bool {
	parser.space()
	if !parser.take('{') {
		return false
	}
	parser.space()
	return true
}

func (parser *secretJSONParser) takeObjectEnd() bool {
	parser.space()
	if !parser.take('}') {
		return false
	}
	parser.space()
	return true
}

func (parser *secretJSONParser) takeColon() bool {
	parser.space()
	if !parser.take(':') {
		return false
	}
	parser.space()
	return true
}

func (parser *secretJSONParser) takeComma() bool {
	parser.space()
	if !parser.take(',') {
		return false
	}
	parser.space()
	return true
}

func (parser *secretJSONParser) finished() bool {
	parser.space()
	return parser.pos == len(parser.data)
}

func (parser *secretJSONParser) boolean() (bool, error) {
	parser.space()
	switch {
	case parser.takeLiteral("true"):
		parser.space()
		return true, nil
	case parser.takeLiteral("false"):
		parser.space()
		return false, nil
	default:
		return false, errInvalidSecretJSON
	}
}

func (parser *secretJSONParser) string() (
	value []byte,
	err error,
) {
	parser.space()
	if !parser.take('"') {
		return nil, errInvalidSecretJSON
	}
	value = make([]byte, 0, 32)
	defer func() {
		if err != nil {
			clear(value)
			value = nil
		}
	}()
	for parser.pos < len(parser.data) {
		current := parser.data[parser.pos]
		parser.pos++
		switch {
		case current == '"':
			parser.space()
			return value, nil
		case current < 0x20:
			return value, errInvalidSecretJSON
		case current == '\\':
			if parser.pos >= len(parser.data) {
				return value, errInvalidSecretJSON
			}
			escaped := parser.data[parser.pos]
			parser.pos++
			switch escaped {
			case '"', '\\', '/':
				value = append(value, escaped)
			case 'b':
				value = append(value, '\b')
			case 'f':
				value = append(value, '\f')
			case 'n':
				value = append(value, '\n')
			case 'r':
				value = append(value, '\r')
			case 't':
				value = append(value, '\t')
			case 'u':
				character, unicodeErr := parser.unicodeEscape()
				if unicodeErr != nil {
					return value, errInvalidSecretJSON
				}
				value = utf8.AppendRune(value, character)
			default:
				return value, errInvalidSecretJSON
			}
		case current < utf8.RuneSelf:
			value = append(value, current)
		default:
			parser.pos--
			character, size := utf8.DecodeRune(parser.data[parser.pos:])
			if character == utf8.RuneError && size == 1 {
				return value, errInvalidSecretJSON
			}
			value = append(value, parser.data[parser.pos:parser.pos+size]...)
			parser.pos += size
		}
	}
	return value, errInvalidSecretJSON
}

func (parser *secretJSONParser) unicodeEscape() (rune, error) {
	first, err := parser.hexQuad()
	if err != nil {
		return 0, errInvalidSecretJSON
	}
	switch {
	case first >= 0xd800 && first <= 0xdbff:
		if parser.pos+2 > len(parser.data) ||
			parser.data[parser.pos] != '\\' ||
			parser.data[parser.pos+1] != 'u' {
			return 0, errInvalidSecretJSON
		}
		parser.pos += 2
		second, secondErr := parser.hexQuad()
		if secondErr != nil || second < 0xdc00 || second > 0xdfff {
			return 0, errInvalidSecretJSON
		}
		return utf16.DecodeRune(rune(first), rune(second)), nil
	case first >= 0xdc00 && first <= 0xdfff:
		return 0, errInvalidSecretJSON
	default:
		return rune(first), nil
	}
}

func (parser *secretJSONParser) hexQuad() (uint16, error) {
	if parser.pos+4 > len(parser.data) {
		return 0, errInvalidSecretJSON
	}
	var result uint16
	for index := 0; index < 4; index++ {
		value, ok := secretHexValue(parser.data[parser.pos+index])
		if !ok {
			return 0, errInvalidSecretJSON
		}
		result = result<<4 | uint16(value)
	}
	parser.pos += 4
	return result, nil
}

func secretHexValue(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func secretASCIIEqual(value []byte, literal string) bool {
	if len(value) != len(literal) {
		return false
	}
	for index := range literal {
		if value[index] != literal[index] {
			return false
		}
	}
	return true
}

func validSecretRefBytes(value []byte) bool {
	if len(value) == 0 ||
		len(value) > 64 ||
		value[0] == '-' ||
		value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-' {
			continue
		}
		return false
	}
	return true
}

func secretActionBytes(value []byte) (string, bool) {
	switch {
	case secretASCIIEqual(value, secrets.ActionSet):
		return secrets.ActionSet, true
	case secretASCIIEqual(value, secrets.ActionRotate):
		return secrets.ActionRotate, true
	case secretASCIIEqual(value, secrets.ActionDelete):
		return secrets.ActionDelete, true
	default:
		return "", false
	}
}

func (parser *secretJSONParser) takeLiteral(literal string) bool {
	if len(parser.data)-parser.pos < len(literal) {
		return false
	}
	for index := range literal {
		if parser.data[parser.pos+index] != literal[index] {
			return false
		}
	}
	parser.pos += len(literal)
	return true
}

func (parser *secretJSONParser) take(value byte) bool {
	if parser.pos >= len(parser.data) ||
		parser.data[parser.pos] != value {
		return false
	}
	parser.pos++
	return true
}

func (parser *secretJSONParser) space() {
	for parser.pos < len(parser.data) {
		switch parser.data[parser.pos] {
		case ' ', '\t', '\n', '\r':
			parser.pos++
		default:
			return
		}
	}
}
