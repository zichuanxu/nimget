package main

import (
	"context"

	"github.com/amemiya02/nimget/internal/pairing"
)

func (d *Desktop) ExtensionPairings() ([]pairing.Record, error) {
	if _, err := d.manager(); err != nil {
		return nil, err
	}
	return d.svc.ListExtensionPairings(context.Background())
}

func (d *Desktop) ApproveExtensionPairing(id, code string) error {
	if _, err := d.manager(); err != nil {
		return err
	}
	return d.svc.ApproveExtensionPairing(context.Background(), id, code)
}

func (d *Desktop) DenyExtensionPairing(id string) error {
	if _, err := d.manager(); err != nil {
		return err
	}
	return d.svc.DenyExtensionPairing(context.Background(), id)
}

func (d *Desktop) RevokeExtensionPairing(id string) error {
	if _, err := d.manager(); err != nil {
		return err
	}
	return d.svc.RevokeExtensionPairing(context.Background(), id)
}
