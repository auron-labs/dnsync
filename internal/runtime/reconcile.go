package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aaronflorey/docker-dns-sync/internal/contracts"
	"github.com/aaronflorey/docker-dns-sync/internal/state"
)

type ReconcileInput struct {
	Output  contracts.Output
	Desired []contracts.DesiredRecord
	Visible []contracts.VisibleRecord
	Owned   state.Snapshot
	Now     func() time.Time
	Grace   time.Duration
	Logger  *slog.Logger
}

type ReconcileResult struct {
	Next        state.Snapshot
	Ambiguities []*ErrVisibleRecordAmbiguous
}

type outputMutationError struct {
	provider contracts.ProviderRef
	err      error
}

func (e outputMutationError) Error() string {
	return e.err.Error()
}

func (e outputMutationError) Unwrap() error {
	return e.err
}

type reconcileUpdateCall struct {
	From contracts.VisibleRecord
	To   contracts.DesiredRecord
}

type partialMutationProgressError struct {
	err error
}

func (e partialMutationProgressError) Error() string {
	return e.err.Error()
}

func (e partialMutationProgressError) Unwrap() error {
	return e.err
}

func ReconcileOutput(ctx context.Context, input ReconcileInput) (ReconcileResult, error) {
	result, _, err := reconcileOutput(ctx, input, nil)
	return result, err
}

func reconcileOutput(ctx context.Context, input ReconcileInput, persist func(state.Snapshot) error) (ReconcileResult, bool, error) {
	nowFn := input.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	now := nowFn().UTC()
	plan := buildReconcilePlanAt(input.Output.Provider(), input.Desired, input.Visible, input.Owned, now, input.Grace)
	logReconcilePlan(ctx, input, plan)
	if len(plan.Ambiguities) > 0 {
		return ReconcileResult{Ambiguities: plan.Ambiguities}, false, plan.Ambiguities[0]
	}

	next, progressed, err := applyReconcilePlan(ctx, input.Output, input.Owned, plan, now, persist)
	if err != nil {
		wrappedErr := outputMutationError{provider: input.Output.Provider(), err: err}
		if progressed {
			return ReconcileResult{Next: next, Ambiguities: plan.Ambiguities}, true, partialMutationProgressError{err: wrappedErr}
		}
		return ReconcileResult{}, false, wrappedErr
	}

	return ReconcileResult{Next: next, Ambiguities: plan.Ambiguities}, false, nil
}

func ReconcileAndPersist(ctx context.Context, store *state.Store, input ReconcileInput) (ReconcileResult, error) {
	result, progressed, err := reconcileOutput(ctx, input, store.Save)
	if err != nil {
		var partialErr partialMutationProgressError
		if progressed && errors.As(err, &partialErr) {
			if input.Logger != nil {
				input.Logger.Warn("persisting partial state snapshot after mutation failure", "output", providerKey(input.Output.Provider()), "records", len(result.Next.ManagedRecords), "error", partialErr)
			}
			// applyReconcilePlan persists after every successful external
			// mutation. This final save preserves deadline-only progress and
			// remains a recovery fallback for callers using older plans.
			if saveErr := store.Save(result.Next); saveErr != nil {
				return ReconcileResult{}, errors.Join(err, fmt.Errorf("persist partial state snapshot: %w", saveErr))
			}
			if input.Logger != nil {
				input.Logger.Warn("persisted partial state snapshot after mutation failure", "output", providerKey(input.Output.Provider()), "records", len(result.Next.ManagedRecords))
			}
		}
		return ReconcileResult{}, err
	}

	if input.Logger != nil {
		input.Logger.Info("persisting state snapshot", "output", providerKey(input.Output.Provider()), "records", len(result.Next.ManagedRecords))
	}

	if err := store.Save(result.Next); err != nil {
		return ReconcileResult{}, err
	}

	if input.Logger != nil {
		input.Logger.Info("persisted state snapshot", "output", providerKey(input.Output.Provider()), "records", len(result.Next.ManagedRecords))
	}

	return result, nil
}

func logReconcilePlan(ctx context.Context, input ReconcileInput, plan reconcilePlan) {
	if input.Logger == nil {
		return
	}

	ownedCount := 0
	for _, record := range input.Owned.ManagedRecords {
		if record.Output == input.Output.Provider() {
			ownedCount++
		}
	}

	logDebug(ctx, input.Logger, "built reconcile plan",
		"output", providerKey(input.Output.Provider()),
		"desired", len(input.Desired),
		"visible", len(input.Visible),
		"owned", ownedCount,
		"creates", len(plan.Creates),
		"updates", len(plan.Updates),
		"deletes", len(plan.Deletes),
		"drops", len(plan.Drops),
		"next_managed", len(plan.NextManaged),
		"blocked_cleanup", len(plan.Blocked),
		"ambiguities", len(plan.Ambiguities),
	)
	for _, record := range plan.Blocked {
		input.Logger.Warn("blocked managed record cleanup without provenance", "output", providerKey(input.Output.Provider()), "hostname", record.Hostname, "answer", record.Answer, "source_id", record.Source.ID)
	}

	for _, record := range plan.Creates {
		logTrace(ctx, input.Logger, "planned record create", "output", providerKey(input.Output.Provider()), "hostname", record.Hostname, "answer", record.Answer, "source_id", record.Source.ID)
	}
	for _, record := range plan.Updates {
		logTrace(ctx, input.Logger, "planned record update", "output", providerKey(input.Output.Provider()), "from_hostname", record.From.Hostname, "from_answer", record.From.Answer, "to_hostname", record.To.Hostname, "to_answer", record.To.Answer, "source_id", record.To.Source.ID)
	}
	for _, record := range plan.Deletes {
		logTrace(ctx, input.Logger, "planned record delete", "output", providerKey(input.Output.Provider()), "hostname", record.Visible.Hostname, "answer", record.Visible.Answer, "source_id", record.Managed.Source.ID)
	}
	for _, record := range plan.Drops {
		logTrace(ctx, input.Logger, "planned managed-state drop", "output", providerKey(input.Output.Provider()), "hostname", record.Hostname, "answer", record.Answer, "source_id", record.Source.ID)
	}
	for _, ambiguity := range plan.Ambiguities {
		logDebug(ctx, input.Logger, "detected visible record ambiguity", "output", ambiguity.Output, "key", ambiguity.Key, "count", ambiguity.Count)
	}
}
