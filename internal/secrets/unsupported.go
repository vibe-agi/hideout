package secrets

import (
	"context"
	"strings"
)

type UnsupportedStore struct {
	ProviderName string
	Reason       string
}

func (store UnsupportedStore) Provider() string {
	if validProviderName(store.ProviderName) {
		return store.ProviderName
	}
	return "unsupported"
}

func (store UnsupportedStore) List(context.Context) ([]Reference, error) {
	return []Reference{}, nil
}

func (store UnsupportedStore) Reference(_ context.Context, ref string) (Reference, error) {
	if err := ValidateRef(ref); err != nil {
		return Reference{}, err
	}
	reason := store.reason()
	reference := Reference{
		Schema:       SecretReferenceSchema,
		Ref:          ref,
		Provider:     store.Provider(),
		Availability: AvailabilityUnavailable,
		Reason:       reason,
	}
	if err := reference.Validate(); err != nil {
		return Reference{}, err
	}
	return reference, nil
}

func (store UnsupportedStore) Set(_ context.Context, request WriteRequest) (Reference, error) {
	if request.Value != nil {
		defer request.Value.Clear()
	}
	if err := request.Validate(); err != nil {
		return Reference{}, err
	}
	return Reference{}, store.unavailableError()
}

func (store UnsupportedStore) Delete(_ context.Context, request DeleteRequest) (Reference, error) {
	if err := request.Validate(); err != nil {
		return Reference{}, err
	}
	return Reference{}, store.unavailableError()
}

func (store UnsupportedStore) Resolve(_ context.Context, ref string) (*Buffer, error) {
	if err := ValidateRef(ref); err != nil {
		return nil, err
	}
	return nil, store.unavailableError()
}

func (store UnsupportedStore) unavailableError() error {
	return &ProviderError{
		Provider: store.Provider(),
		Reason:   store.reason(),
		Cause:    ErrProviderUnavailable,
	}
}

func (store UnsupportedStore) reason() string {
	reason := strings.TrimSpace(store.Reason)
	if reason == "" || !reasonPattern.MatchString(reason) {
		return "provider-unsupported"
	}
	return reason
}

var (
	_ Store           = UnsupportedStore{}
	_ RuntimeResolver = UnsupportedStore{}
)
