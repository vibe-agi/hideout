//go:build darwin && cgo

package secrets

/*
#cgo LDFLAGS: -framework CoreFoundation -framework LocalAuthentication -framework Security

#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

void *hideout_keychain_no_ui_context_create(void);
void hideout_keychain_no_ui_context_release(void *context);

static void hideout_clear_free(void *pointer, size_t length) {
	if (pointer == NULL) {
		return;
	}
	volatile unsigned char *bytes = (volatile unsigned char *)pointer;
	while (length > 0) {
		*bytes++ = 0;
		length--;
	}
	free(pointer);
}

static CFStringRef hideout_string(const char *value) {
	if (value == NULL) {
		return NULL;
	}
	return CFStringCreateWithBytes(
		kCFAllocatorDefault,
		(const UInt8 *)value,
		(CFIndex)strlen(value),
		kCFStringEncodingUTF8,
		false
	);
}

static CFMutableDictionaryRef hideout_query(
	const char *service,
	const char *account,
	OSStatus *status
) {
	*status = errSecSuccess;
	CFStringRef serviceValue = hideout_string(service);
	CFStringRef accountValue = account == NULL ? NULL : hideout_string(account);
	if (serviceValue == NULL || (account != NULL && accountValue == NULL)) {
		if (serviceValue != NULL) {
			CFRelease(serviceValue);
		}
		if (accountValue != NULL) {
			CFRelease(accountValue);
		}
		*status = errSecAllocate;
		return NULL;
	}
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (query == NULL) {
		CFRelease(serviceValue);
		if (accountValue != NULL) {
			CFRelease(accountValue);
		}
		*status = errSecAllocate;
		return NULL;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, serviceValue);
	if (accountValue != NULL) {
		CFDictionarySetValue(query, kSecAttrAccount, accountValue);
	}
	// hideoutd is detached and cannot safely own an authentication dialog.
	// Security.framework otherwise allows UI by default and its synchronous
	// item calls can wait indefinitely for interaction.
	void *authenticationContext =
		hideout_keychain_no_ui_context_create();
	if (authenticationContext == NULL) {
		CFRelease(query);
		*status = errSecAllocate;
		return NULL;
	}
	CFDictionarySetValue(
		query,
		kSecUseAuthenticationContext,
		(CFTypeRef)authenticationContext
	);
	hideout_keychain_no_ui_context_release(authenticationContext);
	CFRelease(serviceValue);
	if (accountValue != NULL) {
		CFRelease(accountValue);
	}
	return query;
}

static OSStatus hideout_keychain_get(
	const char *service,
	const char *account,
	void **output,
	size_t *outputLength
) {
	*output = NULL;
	*outputLength = 0;
	OSStatus status = errSecSuccess;
	CFMutableDictionaryRef query = hideout_query(service, account, &status);
	if (query == NULL) {
		return status;
	}
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	if (status != errSecSuccess) {
		if (result != NULL) {
			CFRelease(result);
		}
		return status;
	}
	if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
		if (result != NULL) {
			CFRelease(result);
		}
		return errSecDecode;
	}
	CFDataRef data = (CFDataRef)result;
	CFIndex length = CFDataGetLength(data);
	if (length <= 0 || (uint64_t)length > (uint64_t)SIZE_MAX) {
		CFRelease(result);
		return errSecDecode;
	}
	void *copy = malloc((size_t)length);
	if (copy == NULL) {
		CFRelease(result);
		return errSecAllocate;
	}
	CFDataGetBytes(
		data,
		CFRangeMake(0, length),
		(UInt8 *)copy
	);
	CFRelease(result);
	*output = copy;
	*outputLength = (size_t)length;
	return errSecSuccess;
}

static OSStatus hideout_keychain_update(
	CFMutableDictionaryRef query,
	CFDataRef data
) {
	CFMutableDictionaryRef updates = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (updates == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(updates, kSecValueData, data);
	OSStatus status = SecItemUpdate(query, updates);
	CFRelease(updates);
	return status;
}

static OSStatus hideout_keychain_add(
	CFMutableDictionaryRef query,
	CFDataRef data
) {
	CFMutableDictionaryRef attributes = CFDictionaryCreateMutableCopy(
		kCFAllocatorDefault,
		0,
		query
	);
	if (attributes == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(attributes, kSecValueData, data);
	OSStatus status = SecItemAdd(attributes, NULL);
	CFRelease(attributes);
	return status;
}

static OSStatus hideout_keychain_put(
	const char *service,
	const char *account,
	const void *input,
	size_t inputLength
) {
	if (input == NULL || inputLength == 0 || inputLength > (size_t)LONG_MAX) {
		return errSecParam;
	}
	OSStatus status = errSecSuccess;
	CFMutableDictionaryRef query = hideout_query(service, account, &status);
	if (query == NULL) {
		return status;
	}
	CFDataRef data = CFDataCreate(
		kCFAllocatorDefault,
		(const UInt8 *)input,
		(CFIndex)inputLength
	);
	if (data == NULL) {
		CFRelease(query);
		return errSecAllocate;
	}
	status = hideout_keychain_update(query, data);
	if (status == errSecItemNotFound) {
		status = hideout_keychain_add(query, data);
		if (status == errSecDuplicateItem) {
			status = hideout_keychain_update(query, data);
		}
	}
	CFRelease(data);
	CFRelease(query);
	return status;
}

static OSStatus hideout_append_account(
	CFDictionaryRef attributes,
	char *buffer,
	size_t capacity,
	size_t *offset
) {
	CFTypeRef raw = CFDictionaryGetValue(attributes, kSecAttrAccount);
	if (raw == NULL || CFGetTypeID(raw) != CFStringGetTypeID()) {
		return errSecDecode;
	}
	char account[257];
	if (!CFStringGetCString(
		(CFStringRef)raw,
		account,
		sizeof(account),
		kCFStringEncodingUTF8
	)) {
		return errSecDecode;
	}
	size_t length = strlen(account);
	if (length == 0 || length > 256 || *offset > capacity ||
		length + 1 > capacity - *offset) {
		return errSecDataTooLarge;
	}
	memcpy(buffer + *offset, account, length);
	*offset += length;
	buffer[*offset] = 0;
	*offset += 1;
	return errSecSuccess;
}

static OSStatus hideout_keychain_accounts(
	const char *service,
	void **output,
	size_t *outputLength
) {
	*output = NULL;
	*outputLength = 0;
	OSStatus status = errSecSuccess;
	CFMutableDictionaryRef query = hideout_query(service, NULL, &status);
	if (query == NULL) {
		return status;
	}
	CFDictionarySetValue(query, kSecReturnAttributes, kCFBooleanTrue);
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitAll);
	CFTypeRef result = NULL;
	status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	if (status == errSecItemNotFound) {
		return errSecSuccess;
	}
	if (status != errSecSuccess) {
		if (result != NULL) {
			CFRelease(result);
		}
		return status;
	}
	CFIndex count = 0;
	bool isArray = result != NULL &&
		CFGetTypeID(result) == CFArrayGetTypeID();
	bool isDictionary = result != NULL &&
		CFGetTypeID(result) == CFDictionaryGetTypeID();
	if (isArray) {
		count = CFArrayGetCount((CFArrayRef)result);
	} else if (isDictionary) {
		count = 1;
	} else {
		if (result != NULL) {
			CFRelease(result);
		}
		return errSecDecode;
	}
	if (count <= 0) {
		CFRelease(result);
		return errSecSuccess;
	}
	if (count > 4096 || (uint64_t)count > SIZE_MAX / 257) {
		CFRelease(result);
		return errSecDataTooLarge;
	}
	size_t capacity = (size_t)count * 257;
	char *buffer = (char *)malloc(capacity);
	if (buffer == NULL) {
		CFRelease(result);
		return errSecAllocate;
	}
	size_t offset = 0;
	for (CFIndex index = 0; index < count; index++) {
		CFTypeRef item = isArray
			? CFArrayGetValueAtIndex((CFArrayRef)result, index)
			: result;
		if (item == NULL ||
			CFGetTypeID(item) != CFDictionaryGetTypeID()) {
			status = errSecDecode;
			break;
		}
		status = hideout_append_account(
			(CFDictionaryRef)item,
			buffer,
			capacity,
			&offset
		);
		if (status != errSecSuccess) {
			break;
		}
	}
	CFRelease(result);
	if (status != errSecSuccess) {
		hideout_clear_free(buffer, capacity);
		return status;
	}
	*output = buffer;
	*outputLength = offset;
	return errSecSuccess;
}

static OSStatus hideout_keychain_delete_item(
	const char *service,
	const char *account
) {
	OSStatus status = errSecSuccess;
	CFMutableDictionaryRef query = hideout_query(service, account, &status);
	if (query == NULL) {
		return status;
	}
	status = SecItemDelete(query);
	CFRelease(query);
	return status;
}

static void hideout_keychain_error_message(
	OSStatus status,
	char *buffer,
	size_t capacity
) {
	if (buffer == NULL || capacity == 0) {
		return;
	}
	buffer[0] = 0;
	CFStringRef message = SecCopyErrorMessageString(status, NULL);
	if (message == NULL) {
		return;
	}
	CFStringGetCString(
		message,
		buffer,
		(CFIndex)capacity,
		kCFStringEncodingUTF8
	);
	CFRelease(message);
}
*/
import "C"

import (
	"bytes"
	"context"
	"fmt"
	"unsafe"
)

type darwinKeychainBackend struct {
	service string
}

type keychainOSStatusError struct {
	Status  int32
	Message string
}

func (err *keychainOSStatusError) Error() string {
	if err == nil {
		return ""
	}
	if err.Message == "" {
		return fmt.Sprintf("Security.framework status %d", err.Status)
	}
	return fmt.Sprintf(
		"Security.framework status %d: %s",
		err.Status,
		err.Message,
	)
}

func NewKeychainStore() RuntimeStore {
	// Hideout ships as a user-context command-line tool. Keep all releases on
	// the file-backed login Keychain: the data-protection Keychain requires an
	// app-like provisioning profile and would make Homebrew/local binaries
	// either unavailable or silently select a different credential namespace.
	return newDarwinKeychainStore(KeychainServiceName)
}

// NewKeychainStoreForRoot isolates non-default Manager stores from the normal
// user's Keychain namespace. The default ~/.hideout store retains the original
// service name for upgrade compatibility.
func NewKeychainStoreForRoot(storeRoot string) RuntimeStore {
	service, err := keychainServiceForStoreRoot(storeRoot)
	if err != nil {
		return UnsupportedStore{
			ProviderName: KeychainProviderName,
			Reason:       "store-root-invalid",
		}
	}
	return newDarwinKeychainStore(service)
}

func newDarwinKeychainStore(service string) *keychainEnvelopeStore {
	return newKeychainEnvelopeStore(
		&darwinKeychainBackend{service: service},
		nil,
	)
}

func (backend *darwinKeychainBackend) Accounts(
	ctx context.Context,
) ([]string, error) {
	if err := checkSecretContext(ctx); err != nil {
		return nil, err
	}
	service, err := backend.serviceCString()
	if err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(service))
	var output unsafe.Pointer
	var outputLength C.size_t
	status := C.hideout_keychain_accounts(
		service,
		&output,
		&outputLength,
	)
	if output != nil {
		defer C.hideout_clear_free(output, outputLength)
	}
	if err := darwinKeychainStatusError(status); err != nil {
		return nil, err
	}
	if outputLength == 0 {
		return []string{}, checkSecretContext(ctx)
	}
	if uint64(outputLength) > uint64(maxKeychainReferences*257) {
		return nil, ErrSecretEnvelopeCorrupt
	}
	data := C.GoBytes(output, C.int(outputLength))
	defer clear(data)
	rawAccounts := bytes.Split(data, []byte{0})
	accounts := make([]string, 0, len(rawAccounts))
	for _, raw := range rawAccounts {
		if len(raw) == 0 {
			continue
		}
		accounts = append(accounts, string(raw))
	}
	return accounts, checkSecretContext(ctx)
}

func (backend *darwinKeychainBackend) Get(
	ctx context.Context,
	ref string,
) ([]byte, error) {
	if err := checkSecretContext(ctx); err != nil {
		return nil, err
	}
	service, err := backend.serviceCString()
	if err != nil {
		return nil, err
	}
	defer C.free(unsafe.Pointer(service))
	account := C.CString(ref)
	if account == nil {
		return nil, ErrProviderUnavailable
	}
	defer C.free(unsafe.Pointer(account))
	var output unsafe.Pointer
	var outputLength C.size_t
	status := C.hideout_keychain_get(
		service,
		account,
		&output,
		&outputLength,
	)
	if output != nil {
		defer C.hideout_clear_free(output, outputLength)
	}
	if err := darwinKeychainStatusError(status); err != nil {
		return nil, err
	}
	if outputLength == 0 ||
		uint64(outputLength) > uint64(maxKeychainEnvelopeBytes) {
		return nil, ErrSecretEnvelopeCorrupt
	}
	data := C.GoBytes(output, C.int(outputLength))
	if err := checkSecretContext(ctx); err != nil {
		clear(data)
		return nil, err
	}
	return data, nil
}

func (backend *darwinKeychainBackend) Put(
	ctx context.Context,
	ref string,
	data []byte,
) error {
	if err := checkSecretContext(ctx); err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxKeychainEnvelopeBytes {
		return ErrSecretEnvelopeCorrupt
	}
	service, err := backend.serviceCString()
	if err != nil {
		return err
	}
	defer C.free(unsafe.Pointer(service))
	account := C.CString(ref)
	if account == nil {
		return ErrProviderUnavailable
	}
	defer C.free(unsafe.Pointer(account))
	input := C.CBytes(data)
	if input == nil {
		return ErrProviderUnavailable
	}
	defer C.hideout_clear_free(input, C.size_t(len(data)))
	status := C.hideout_keychain_put(
		service,
		account,
		input,
		C.size_t(len(data)),
	)
	if err := darwinKeychainStatusError(status); err != nil {
		return err
	}
	return checkSecretContext(ctx)
}

func (backend *darwinKeychainBackend) deleteItem(
	ctx context.Context,
	ref string,
) error {
	if err := checkSecretContext(ctx); err != nil {
		return err
	}
	service, err := backend.serviceCString()
	if err != nil {
		return err
	}
	defer C.free(unsafe.Pointer(service))
	account := C.CString(ref)
	if account == nil {
		return ErrProviderUnavailable
	}
	defer C.free(unsafe.Pointer(account))
	status := C.hideout_keychain_delete_item(service, account)
	if status == C.errSecItemNotFound {
		return nil
	}
	return darwinKeychainStatusError(status)
}

func (backend *darwinKeychainBackend) serviceCString() (*C.char, error) {
	if backend == nil ||
		backend.service == "" ||
		len(backend.service) > 256 {
		return nil, ErrProviderUnavailable
	}
	value := C.CString(backend.service)
	if value == nil {
		return nil, ErrProviderUnavailable
	}
	return value, nil
}

func darwinKeychainStatusError(status C.OSStatus) error {
	switch status {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return errKeychainItemMissing
	case C.errSecInteractionNotAllowed,
		C.errSecInteractionRequired,
		C.errSecAuthFailed,
		C.errSecUserCanceled:
		return ErrSecretLocked
	case C.errSecNotAvailable,
		C.errSecNoDefaultKeychain,
		C.errSecNoSuchKeychain,
		C.errSecMissingEntitlement:
		return ErrProviderUnavailable
	case C.errSecDecode,
		C.errSecDataTooLarge:
		return ErrSecretEnvelopeCorrupt
	default:
		var message [512]C.char
		C.hideout_keychain_error_message(
			status,
			&message[0],
			C.size_t(len(message)),
		)
		return &keychainOSStatusError{
			Status:  int32(status),
			Message: C.GoString(&message[0]),
		}
	}
}

var (
	_ keychainItemBackend = (*darwinKeychainBackend)(nil)
	_ error               = (*keychainOSStatusError)(nil)
)
