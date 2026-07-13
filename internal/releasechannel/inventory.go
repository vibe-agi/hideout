package releasechannel

import (
	"errors"
	"fmt"
	"time"
)

type PublishedInventory struct {
	Schema      string          `json:"schema"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Current     *InventoryEntry `json:"current"`
}

type InventoryEntry struct {
	Version       string          `json:"version"`
	Tag           string          `json:"tag"`
	Maturity      string          `json:"maturity"`
	Platform      string          `json:"platform"`
	Backend       string          `json:"backend"`
	Package       PackageIdentity `json:"package"`
	ReleaseURL    string          `json:"releaseURL"`
	ReceiptSHA256 string          `json:"receiptSHA256"`
	SupportMatrix string          `json:"supportMatrix"`
	NonClaims     []string        `json:"nonClaims"`
}

func (i PublishedInventory) Validate() error {
	if i.Schema != PublishedInventorySchema || i.GeneratedAt.IsZero() {
		return errors.New("published inventory schema and generatedAt are required")
	}
	if i.Current == nil {
		return nil
	}
	c := i.Current
	if err := ValidateTag(c.Version, c.Tag); err != nil {
		return err
	}
	if c.Maturity != "public-supervised-alpha" || c.Platform != "darwin/arm64" || c.Backend != "lima" || c.ReleaseURL == "" || !IsSHA256(c.ReceiptSHA256) || c.SupportMatrix == "" || len(c.NonClaims) == 0 {
		return errors.New("published inventory current release is incomplete")
	}
	if err := c.Package.Validate(); err != nil {
		return fmt.Errorf("published inventory package: %w", err)
	}
	return nil
}
