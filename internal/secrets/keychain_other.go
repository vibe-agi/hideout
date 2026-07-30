//go:build !darwin || !cgo

package secrets

func NewKeychainStore() RuntimeStore {
	return UnsupportedStore{
		ProviderName: KeychainProviderName,
		Reason:       "platform-unsupported",
	}
}

func NewKeychainStoreForRoot(string) RuntimeStore {
	return NewKeychainStore()
}
