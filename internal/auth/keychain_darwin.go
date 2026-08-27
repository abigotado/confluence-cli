//go:build darwin && cgo

package auth

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>

static CFMutableDictionaryRef confluence_query(CFStringRef service, CFStringRef account) {
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (query == NULL) {
		return NULL;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecAttrService, service);
	CFDictionarySetValue(query, kSecAttrAccount, account);
	CFDictionarySetValue(query, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	return query;
}

static OSStatus confluence_resolve_item(CFStringRef service, CFStringRef account, CFTypeRef *result) {
	CFMutableDictionaryRef query = confluence_query(service, account);
	if (query == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFDictionarySetValue(query, kSecReturnRef, kCFBooleanTrue);
	OSStatus status = SecItemCopyMatching(query, result);
	CFRelease(query);
	return status;
}

static OSStatus confluence_load(CFStringRef service, CFStringRef account, CFTypeRef *result) {
	CFMutableDictionaryRef query = confluence_query(service, account);
	if (query == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
	CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
	OSStatus status = SecItemCopyMatching(query, result);
	CFRelease(query);
	return status;
}

static CFMutableDictionaryRef confluence_item_query(SecKeychainItemRef item) {
	CFMutableDictionaryRef query = CFDictionaryCreateMutable(
		kCFAllocatorDefault,
		0,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (query == NULL) {
		return NULL;
	}
	const void *values[] = { item };
	CFArrayRef items = CFArrayCreate(
		kCFAllocatorDefault,
		values,
		1,
		&kCFTypeArrayCallBacks
	);
	if (items == NULL) {
		CFRelease(query);
		return NULL;
	}
	CFDictionarySetValue(query, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(query, kSecMatchItemList, items);
	CFDictionarySetValue(query, kSecUseAuthenticationUI, kSecUseAuthenticationUIFail);
	CFRelease(items);
	return query;
}

static OSStatus confluence_add(CFStringRef service, CFStringRef account, CFDataRef value, SecAccessRef access) {
	CFMutableDictionaryRef attributes = confluence_query(service, account);
	if (attributes == NULL) {
		return errSecAllocate;
	}
	CFDictionarySetValue(attributes, kSecValueData, value);
	CFDictionarySetValue(attributes, kSecAttrAccess, access);
	OSStatus status = SecItemAdd(attributes, NULL);
	CFRelease(attributes);
	return status;
}

static OSStatus confluence_update_item(SecKeychainItemRef item, CFDataRef value) {
	CFMutableDictionaryRef query = confluence_item_query(item);
	if (query == NULL) {
		return errSecAllocate;
	}
	const void *keys[] = { kSecValueData };
	const void *values[] = { value };
	CFDictionaryRef attributes = CFDictionaryCreate(
		kCFAllocatorDefault,
		keys,
		values,
		1,
		&kCFTypeDictionaryKeyCallBacks,
		&kCFTypeDictionaryValueCallBacks
	);
	if (attributes == NULL) {
		CFRelease(query);
		return errSecAllocate;
	}
	OSStatus status = SecItemUpdate(query, attributes);
	CFRelease(attributes);
	CFRelease(query);
	return status;
}

static OSStatus confluence_delete_item(SecKeychainItemRef item) {
	CFMutableDictionaryRef query = confluence_item_query(item);
	if (query == NULL) {
		return errSecAllocate;
	}
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status;
}

static OSStatus confluence_create_access(CFStringRef description, SecAccessRef *access) {
	CFArrayRef trustedApplications = CFArrayCreate(
		kCFAllocatorDefault,
		NULL,
		0,
		&kCFTypeArrayCallBacks
	);
	if (trustedApplications == NULL) {
		return errSecAllocate;
	}
	OSStatus status = SecAccessCreate(description, trustedApplications, access);
	CFRelease(trustedApplications);
	return status;
}

static OSStatus confluence_copy_decrypt_acl(
	SecAccessRef access,
	CFArrayRef *aclList,
	SecACLRef *acl,
	CFArrayRef *applicationList,
	CFStringRef *description,
	SecKeychainPromptSelector *promptSelector,
	CFIndex *count
) {
	*aclList = SecAccessCopyMatchingACLList(access, kSecACLAuthorizationDecrypt);
	if (*aclList == NULL) {
		return errSecInternalComponent;
	}
	*count = CFArrayGetCount(*aclList);
	if (*count != 1) {
		return errSecSuccess;
	}
	*acl = (SecACLRef)CFArrayGetValueAtIndex(*aclList, 0);
	return SecACLCopyContents(*acl, applicationList, description, promptSelector);
}

static OSStatus confluence_set_allow_any_acl(
	SecACLRef acl,
	CFStringRef description,
	SecKeychainPromptSelector promptSelector
) {
	return SecACLSetContents(acl, NULL, description, promptSelector);
}
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"

	"github.com/abigotado/confluence-cli/internal/profile"
)

const (
	nullSecurityRef                   = 0
	keychainStatusSuccess       int64 = int64(C.errSecSuccess)
	keychainStatusItemNotFound  int64 = int64(C.errSecItemNotFound)
	keychainStatusNoInteraction int64 = int64(C.errSecInteractionNotAllowed)
	keychainStatusUserCanceled  int64 = int64(C.errSecUserCanceled)
	keychainPromptRequirePass         = uint16(C.kSecKeychainPromptRequirePassphase)
)

type decryptACLCountError struct {
	count int
}

func (e *decryptACLCountError) Error() string {
	return fmt.Sprintf("keychain access has %d decrypt ACL entries; expected exactly one", e.count)
}

// KeychainStore stores Confluence tokens in the macOS login Keychain.
type KeychainStore struct{}

var _ CredentialAccessStore = KeychainStore{}

// Exists reports whether the exact profile account has a token without
// requesting or returning the secret value.
func (KeychainStore) Exists(ctx context.Context, profileName string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := profile.ValidateName(profileName); err != nil {
		return false, err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return false, err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(profileName)
	if err != nil {
		return false, err
	}
	defer releaseAccount()

	_, releaseItem, status := resolveKeychainItem(service, account)
	switch status {
	case C.errSecSuccess:
		releaseItem()
		return true, nil
	case C.errSecItemNotFound:
		return false, nil
	default:
		return false, translateStatus("resolve", status)
	}
}

// Load retrieves the token for the exact profile account without allowing UI.
func (KeychainStore) Load(ctx context.Context, profileName string) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	if err := profile.ValidateName(profileName); err != nil {
		return Credential{}, err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return Credential{}, err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(profileName)
	if err != nil {
		return Credential{}, err
	}
	defer releaseAccount()

	item, releaseItem, status := resolveKeychainItem(service, account)
	if status != C.errSecSuccess {
		return Credential{}, translateStatus("resolve", status)
	}
	defer releaseItem()

	var result C.CFTypeRef
	err = runCompatibleKeychainOperation(
		func() (bool, error) { return itemAccessIsCompatible(item) },
		func() error {
			status = C.confluence_load(service, account, &result)
			if status != C.errSecSuccess {
				releaseCFType(result)
			}
			return translateStatus("load", status)
		},
	)
	if err != nil {
		return Credential{}, err
	}
	if result == nullSecurityRef || C.CFGetTypeID(result) != C.CFDataGetTypeID() {
		releaseCFType(result)
		return Credential{}, &StatusError{Operation: "load", Status: int64(C.errSecInternalComponent)}
	}
	defer C.CFRelease(result)
	data := C.CFDataRef(result)
	length := C.CFDataGetLength(data)
	if length <= 0 || length > C.CFIndex(maxStoredCredentialBytes) {
		return Credential{}, fmt.Errorf("stored credential is invalid: %w", ErrInvalidToken)
	}
	bytes := C.CFDataGetBytePtr(data)
	if bytes == nil {
		return Credential{}, &StatusError{Operation: "load", Status: int64(C.errSecInternalComponent)}
	}
	value := C.GoBytes(unsafe.Pointer(bytes), C.int(length))
	return decodeCredentialValue(value)
}

// Save creates or updates the token for the exact profile account.
func (KeychainStore) Save(ctx context.Context, profileName string, credential Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := profile.ValidateName(profileName); err != nil {
		return err
	}
	encoded, err := encodeCredentialValue(credential)
	if err != nil {
		return err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(profileName)
	if err != nil {
		return err
	}
	defer releaseAccount()
	value := C.CFDataCreate(C.kCFAllocatorDefault, (*C.UInt8)(unsafe.Pointer(unsafe.SliceData(encoded))), C.CFIndex(len(encoded)))
	if value == 0 {
		return &StatusError{Operation: "save", Status: int64(C.errSecAllocate)}
	}
	defer C.CFRelease(C.CFTypeRef(value))
	return saveExactKeychainItem(service, account, value)
}

// MigrateKeychain normalizes the exact profile item's decrypt ACL without
// requesting or returning its credential value. The access-policy update may
// ask for authorization in an interactive macOS session.
func (KeychainStore) MigrateKeychain(ctx context.Context, profileName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := profile.ValidateName(profileName); err != nil {
		return err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(profileName)
	if err != nil {
		return err
	}
	defer releaseAccount()

	item, releaseItem, status := resolveKeychainItem(service, account)
	if status != C.errSecSuccess {
		return translateStatus("resolve", status)
	}
	defer releaseItem()
	if err := ctx.Err(); err != nil {
		return err
	}
	return migrateExistingAccess(item)
}

// Delete removes the token for the exact profile account without allowing UI.
func (KeychainStore) Delete(ctx context.Context, profileName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := profile.ValidateName(profileName); err != nil {
		return err
	}
	service, releaseService, err := makeCFString(KeychainService)
	if err != nil {
		return err
	}
	defer releaseService()
	account, releaseAccount, err := makeCFString(profileName)
	if err != nil {
		return err
	}
	defer releaseAccount()
	item, releaseItem, status := resolveKeychainItem(service, account)
	if status == C.errSecItemNotFound {
		return nil
	}
	if status != C.errSecSuccess {
		return translateStatus("resolve", status)
	}
	defer releaseItem()
	return runCompatibleKeychainOperation(
		func() (bool, error) { return itemAccessIsCompatible(item) },
		func() error { return translateStatus("delete", C.confluence_delete_item(item)) },
	)
}

func saveExactKeychainItem(service, account C.CFStringRef, value C.CFDataRef) error {
	for attempt := 0; attempt < 2; attempt++ {
		item, releaseItem, status := resolveKeychainItem(service, account)
		switch status {
		case C.errSecSuccess:
			err := runCompatibleKeychainOperation(
				func() (bool, error) { return itemAccessIsCompatible(item) },
				func() error { return translateStatus("save", C.confluence_update_item(item, value)) },
			)
			releaseItem()
			return err
		case C.errSecItemNotFound:
			access, releaseAccess, err := makeAllowAnyAccess(service)
			if err != nil {
				return err
			}
			status = C.confluence_add(service, account, value, access)
			releaseAccess()
			if status == C.errSecSuccess {
				return nil
			}
			if status == C.errSecDuplicateItem && attempt == 0 {
				continue
			}
			return translateStatus("save", status)
		default:
			return translateStatus("resolve", status)
		}
	}
	return &StatusError{Operation: "save", Status: int64(C.errSecInternalComponent)}
}

func makeCFString(value string) (C.CFStringRef, func(), error) {
	bytes := unsafe.StringData(value)
	result := C.CFStringCreateWithBytes(
		C.kCFAllocatorDefault,
		(*C.UInt8)(unsafe.Pointer(bytes)),
		C.CFIndex(len(value)),
		C.kCFStringEncodingUTF8,
		C.false,
	)
	if result == 0 {
		return 0, func() {}, &StatusError{Operation: "allocate", Status: int64(C.errSecAllocate)}
	}
	return result, func() { C.CFRelease(C.CFTypeRef(result)) }, nil
}

func makeAllowAnyAccess(description C.CFStringRef) (C.SecAccessRef, func(), error) {
	var access C.SecAccessRef
	status := C.confluence_create_access(description, &access)
	if status != C.errSecSuccess {
		releaseCFType(C.CFTypeRef(access))
		return nullSecurityRef, func() {}, translateStatus("create access", status)
	}
	if access == nullSecurityRef {
		return nullSecurityRef, func() {}, &StatusError{Operation: "create access", Status: int64(C.errSecInternalComponent)}
	}
	if _, err := normalizeAllowAnyAccess(access); err != nil {
		C.CFRelease(C.CFTypeRef(access))
		return nullSecurityRef, func() {}, err
	}
	return access, func() { C.CFRelease(C.CFTypeRef(access)) }, nil
}

func normalizeAllowAnyAccess(access C.SecAccessRef) (bool, error) {
	var aclList C.CFArrayRef
	var acl C.SecACLRef
	var applicationList C.CFArrayRef
	var description C.CFStringRef
	var promptSelector C.SecKeychainPromptSelector
	var count C.CFIndex
	status := C.confluence_copy_decrypt_acl(
		access,
		&aclList,
		&acl,
		&applicationList,
		&description,
		&promptSelector,
		&count,
	)
	defer releaseCFType(C.CFTypeRef(aclList))
	defer releaseCFType(C.CFTypeRef(applicationList))
	defer releaseCFType(C.CFTypeRef(description))
	if status != C.errSecSuccess {
		return false, translateStatus("read access", status)
	}
	flags, changed, err := normalizeAllowAnyACL(int(count), applicationList == nullSecurityRef, uint16(promptSelector))
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	status = C.confluence_set_allow_any_acl(acl, description, C.SecKeychainPromptSelector(flags))
	if status != C.errSecSuccess {
		return false, translateStatus("normalize access", status)
	}
	return true, nil
}

func allowAnyAccessIsCompatible(access C.SecAccessRef) (bool, error) {
	var aclList C.CFArrayRef
	var acl C.SecACLRef
	var applicationList C.CFArrayRef
	var description C.CFStringRef
	var promptSelector C.SecKeychainPromptSelector
	var count C.CFIndex
	status := C.confluence_copy_decrypt_acl(
		access,
		&aclList,
		&acl,
		&applicationList,
		&description,
		&promptSelector,
		&count,
	)
	defer releaseCFType(C.CFTypeRef(aclList))
	defer releaseCFType(C.CFTypeRef(applicationList))
	defer releaseCFType(C.CFTypeRef(description))
	if status != C.errSecSuccess {
		return false, translateStatus("read access", status)
	}
	_, changed, err := normalizeAllowAnyACL(int(count), applicationList == nullSecurityRef, uint16(promptSelector))
	if err != nil {
		return false, err
	}
	return !changed, nil
}

func normalizeAllowAnyACL(decryptACLCount int, applicationListIsNil bool, promptFlags uint16) (uint16, bool, error) {
	if decryptACLCount != 1 {
		return 0, false, &decryptACLCountError{count: decryptACLCount}
	}
	normalizedFlags := promptFlags &^ keychainPromptRequirePass
	changed := !applicationListIsNil || normalizedFlags != promptFlags
	return normalizedFlags, changed, nil
}

func migrateExistingAccess(item C.SecKeychainItemRef) error {
	access, releaseAccess, err := copyItemAccess(item)
	if err != nil {
		return err
	}
	defer releaseAccess()
	changed, err := normalizeAllowAnyAccess(access)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	status := C.SecKeychainItemSetAccess(item, access)
	return translateStatus("set access", status)
}

func itemAccessIsCompatible(item C.SecKeychainItemRef) (bool, error) {
	access, releaseAccess, err := copyItemAccess(item)
	if err != nil {
		return false, err
	}
	defer releaseAccess()
	return allowAnyAccessIsCompatible(access)
}

func runCompatibleKeychainOperation(check func() (bool, error), operation func() error) error {
	compatible, err := check()
	if err != nil {
		return err
	}
	if !compatible {
		return ErrKeychainMigrationRequired
	}
	return operation()
}

func copyItemAccess(item C.SecKeychainItemRef) (C.SecAccessRef, func(), error) {
	var access C.SecAccessRef
	status := C.SecKeychainItemCopyAccess(item, &access)
	if status != C.errSecSuccess {
		releaseCFType(C.CFTypeRef(access))
		return nullSecurityRef, func() {}, translateStatus("copy access", status)
	}
	if access == nullSecurityRef {
		return nullSecurityRef, func() {}, &StatusError{Operation: "copy access", Status: int64(C.errSecInternalComponent)}
	}
	return access, func() { C.CFRelease(C.CFTypeRef(access)) }, nil
}

func resolveKeychainItem(service, account C.CFStringRef) (C.SecKeychainItemRef, func(), C.OSStatus) {
	var result C.CFTypeRef
	status := C.confluence_resolve_item(service, account, &result)
	if status != C.errSecSuccess {
		releaseCFType(result)
		return nullSecurityRef, func() {}, status
	}
	if result == nullSecurityRef || C.CFGetTypeID(result) != C.SecKeychainItemGetTypeID() {
		releaseCFType(result)
		return nullSecurityRef, func() {}, C.errSecInternalComponent
	}
	return C.SecKeychainItemRef(result), func() { C.CFRelease(result) }, C.errSecSuccess
}

func releaseCFType(value C.CFTypeRef) {
	if value != nullSecurityRef {
		C.CFRelease(value)
	}
}

func translateStatus(operation string, status C.OSStatus) error {
	return translateStatusCode(operation, int64(status))
}

func translateStatusCode(operation string, status int64) error {
	switch status {
	case keychainStatusSuccess:
		return nil
	case keychainStatusItemNotFound:
		return ErrNotFound
	case keychainStatusNoInteraction:
		return ErrInteractionNotAllowed
	case keychainStatusUserCanceled:
		return ErrKeychainMigrationCanceled
	default:
		return &StatusError{Operation: operation, Status: status}
	}
}
