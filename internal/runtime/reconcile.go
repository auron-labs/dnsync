package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/auron-labs/dnsync/internal/contracts"
	"github.com/auron-labs/dnsync/internal/state"
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
	Ambiguities []*VisibleRecordAmbiguousError
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

func ReconcileOutput(ctx context.Context, input ReconcileInput) (ReconcileResult, error) {
	return reconcileOutput(ctx, input, nil)
}

// reconcileOutput applies the plan for one output. On a mutation error the
// result still holds the next snapshot: successful mutations, removal
// deadlines, and drops all belong in state, and failed records keep their
// previous entries.
func reconcileOutput(ctx context.Context, input ReconcileInput, persist func(state.Snapshot) error) (ReconcileResult, error) {
	nowFn := input.Now
	if nowFn == nil {
		nowFn = time.Now
	}

	now := nowFn().UTC()
	plan := buildReconcilePlanAt(input.Output.Provider(), input.Desired, input.Visible, input.Owned, now, input.Grace)
	logReconcilePlan(ctx, input, plan)

	// The plan leaves ambiguous records untouched, so the rest is still applied.
	next, _, err := applyReconcilePlan(ctx, input.Output, input.Owned, plan, now, persist)
	result := ReconcileResult{Next: next, Ambiguities: plan.Ambiguities}
	if err != nil {
		return result, outputMutationError{provider: input.Output.Provider(), err: err}
	}
	return result, nil
}

// ReconcileAndPersist saves the next snapshot even when a mutation failed, then
// returns that failure with the result.
func ReconcileAndPersist(ctx context.Context, store *state.Store, input ReconcileInput) (ReconcileResult, error) {
	result, err := reconcileOutput(ctx, input, store.Save)

	if input.Logger != nil {
		input.Logger.Info("persisting state snapshot", "output", providerKey(input.Output.Provider()), "records", len(result.Next.ManagedRecords))
	}
	if saveErr := store.Save(result.Next); saveErr != nil {
		return ReconcileResult{}, errors.Join(err, fmt.Errorf("save state snapshot: %w", saveErr))
	}
	if input.Logger != nil {
		input.Logger.Info("persisted state snapshot", "output", providerKey(input.Output.Provider()), "records", len(result.Next.ManagedRecords))
	}

	return result, err
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
		input.Logger.Warn("skipping duplicate visible records", "output", ambiguity.Output, "key", ambiguity.Key, "count", ambiguity.Count)
	}
}
