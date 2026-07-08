// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
)

// ReadHistoryInteractor implements the ReadHistory use case: list a release's
// retained revisions via the RevisionStore (SPEC §4.2).
type ReadHistoryInteractor struct {
	store     out.RevisionRepository
	target    string
	namespace string
}

var _ in.ReadHistoryUseCase = (*ReadHistoryInteractor)(nil)

// NewReadHistoryInteractor injects the RevisionStore port and target context.
func NewReadHistoryInteractor(store out.RevisionRepository, target, namespace string) *ReadHistoryInteractor {
	if target == "" {
		target = "files"
	}
	return &ReadHistoryInteractor{store: store, target: target, namespace: namespace}
}

func (r *ReadHistoryInteractor) ReadHistory(input in.ReadHistoryInput) (in.ReadHistoryOutput, error) {
	revs, err := r.store.History(input.ReleaseName, r.target, r.namespace)
	if err != nil {
		return in.ReadHistoryOutput{}, err
	}
	last := 0
	if len(revs) > 0 {
		last = revs[len(revs)-1].Number
	}
	return in.ReadHistoryOutput{Result: resultOf(input.ReleaseName, last)}, nil
}
