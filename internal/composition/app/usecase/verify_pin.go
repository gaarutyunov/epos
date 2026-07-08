// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
	"github.com/gaarutyunov/epos/internal/composition/app/port/out"
	"github.com/gaarutyunov/epos/internal/composition/domain"
)

// VerifyPinInteractor implements the VerifyPin use case: it re-resolves a pulled
// layer and compares the captured pin against the expected one — any mismatch is
// a hard error (SPEC §9.7).
type VerifyPinInteractor struct {
	source out.LayerSource
}

var _ in.VerifyPinUseCase = (*VerifyPinInteractor)(nil)

// NewVerifyPinInteractor injects the LayerSource driven port.
func NewVerifyPinInteractor(source out.LayerSource) *VerifyPinInteractor {
	return &VerifyPinInteractor{source: source}
}

func (v *VerifyPinInteractor) VerifyPin(input in.VerifyPinInput) (in.VerifyPinOutput, error) {
	expected := input.Expected
	// Re-resolve the same source (carried on the expected pin) and compare.
	got, err := v.source.LayerSource(domain.Layer{Name: expected.Name, Pin: expected.Pin})
	if err != nil {
		return in.VerifyPinOutput{Ok: false}, err
	}
	ok := got.Pin.Digest == expected.Pin.Digest &&
		got.Pin.Commit == expected.Pin.Commit &&
		got.Pin.TreeSha == expected.Pin.TreeSha
	return in.VerifyPinOutput{Ok: ok}, nil
}
