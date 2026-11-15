package runtime

import (
	"sort"
	"strings"
	"time"

	"github.com/auron-labs/dnsync/internal/contracts"
	"github.com/auron-labs/dnsync/internal/state"
)

type reconcilePlan struct {
	Creates      []contracts.DesiredRecord
	Updates      []reconcileUpdateCall
	Deletes      []reconcileDeleteCall
	Drops        []state.ManagedRecord
	CreateDrops  map[string]state.ManagedRecord
	UpdateDrops  map[string]state.ManagedRecord
	KeepDrops    map[string]state.ManagedRecord
	Ambiguities  []*ErrVisibleRecordAmbiguous
	Blocked      []state.ManagedRecord
	NextManaged  []state.ManagedRecord
	AppliedIndex map[string]state.ManagedRecord
}

type reconcileDeleteCall struct {
	Visible contracts.VisibleRecord
	Managed state.ManagedRecord
}

func buildReconcilePlan(output contracts.ProviderRef, desired []contracts.DesiredRecord, visible []contracts.VisibleRecord, owned state.Snapshot) reconcilePlan {
	return buildReconcilePlanAt(output, desired, visible, owned, time.Now().UTC(), 0)
}

func buildReconcilePlanAt(output contracts.ProviderRef, desired []contracts.DesiredRecord, visible []contracts.VisibleRecord, owned state.Snapshot, now time.Time, grace time.Duration) reconcilePlan {
	pl := reconcilePlan{
		AppliedIndex: make(map[string]state.ManagedRecord),
		CreateDrops:  make(map[string]state.ManagedRecord),
		UpdateDrops:  make(map[string]state.ManagedRecord),
		KeepDrops:    make(map[string]state.ManagedRecord),
	}

	visibleByKey := make(map[string][]contracts.VisibleRecord)
	visibleByHostname := make(map[string][]contracts.VisibleRecord)
	for _, v := range visible {
		if v.Output != output {
			continue
		}
		k := visibleRecordKey(v.Hostname, v.Answer)
		visibleByKey[k] = append(visibleByKey[k], v)
		hostname := normalizeHostname(v.Hostname)
		visibleByHostname[hostname] = append(visibleByHostname[hostname], v)
	}

	for k, records := range visibleByKey {
		if len(records) > 1 {
			pl.Ambiguities = append(pl.Ambiguities, &ErrVisibleRecordAmbiguous{Output: output.Type + "/" + output.Name, Key: k, Count: len(records)})
		}
	}

	desiredByLineage := make(map[string]contracts.DesiredRecord)
	for _, d := range desired {
		lineage := ownedLineageKey(output.Type, output.Name, d.Source.Provider.Type, d.Source.Provider.Name, d.Source.ID, d.Hostname)
		desiredByLineage[lineage] = d
	}
	desiredLineages := make([]string, 0, len(desiredByLineage))
	for lineage := range desiredByLineage {
		desiredLineages = append(desiredLineages, lineage)
	}
	sort.Strings(desiredLineages)

	ownedByLineage := make(map[string]state.ManagedRecord)
	ownedByDisplayLineage := make(map[string][]state.ManagedRecord)
	ownedByHostname := make(map[string][]state.ManagedRecord)
	for _, m := range owned.ManagedRecords {
		if m.Output != output {
			continue
		}
		lineage := ownedLineageKey(output.Type, output.Name, m.Source.Provider.Type, m.Source.Provider.Name, m.Source.ID, m.Hostname)
		ownedByLineage[lineage] = m
		displayLineage := ownedDisplayLineageKey(output, m.Source, m.Hostname)
		ownedByDisplayLineage[displayLineage] = append(ownedByDisplayLineage[displayLineage], m)
		hostname := normalizeHostname(m.Hostname)
		ownedByHostname[hostname] = append(ownedByHostname[hostname], m)
	}
	matchedOwnedLineages := make(map[string]struct{}, len(ownedByLineage))
	plannedCreateHostnames := make(map[string]struct{})
	matchedVisibleKeys := make(map[string]struct{})

	for _, lineage := range desiredLineages {
		d := desiredByLineage[lineage]
		matchedByHostnameOnly := false
		ownedRecord, ok := ownedByLineage[lineage]
		if !ok {
			candidates := ownedByDisplayLineage[ownedDisplayLineageKey(output, d.Source, d.Hostname)]
			if len(candidates) == 1 {
				ownedRecord = candidates[0]
				ok = true
			}
		}
		if !ok {
			candidates := ownedByHostname[normalizeHostname(d.Hostname)]
			if len(candidates) == 1 {
				ownedRecord = candidates[0]
				ok = true
				matchedByHostnameOnly = true
			}
		}
		if ok {
			ownedLineage := managedRecordLineageKey(ownedRecord)
			matchedOwnedLineages[ownedLineage] = struct{}{}
			hostnameMatches := visibleByHostname[normalizeHostname(d.Hostname)]
			oldKey := visibleRecordKey(ownedRecord.Hostname, ownedRecord.Answer)
			newKey := visibleRecordKey(d.Hostname, d.Answer)
			if len(visibleByKey[oldKey]) == 1 && visibleRecordKey(d.Hostname, d.Answer) != oldKey {
				if canUpdateOwnedVisibleRecord(owned.InstallationID, ownedRecord, visibleByKey[oldKey][0], matchedByHostnameOnly) {
					if ownedLineage != lineage {
						pl.UpdateDrops[lineage] = ownedRecord
					}
					matchedVisibleKeys[oldKey] = struct{}{}
					pl.Updates = append(pl.Updates, reconcileUpdateCall{From: visibleByKey[oldKey][0], To: d})
					continue
				}
				continue
			}
			if len(visibleByKey[oldKey]) > 1 {
				continue
			}
			if len(hostnameMatches) == 1 && newKey != visibleRecordKey(hostnameMatches[0].Hostname, hostnameMatches[0].Answer) {
				if canUpdateOwnedVisibleRecord(owned.InstallationID, ownedRecord, hostnameMatches[0], matchedByHostnameOnly) {
					if ownedLineage != lineage {
						pl.UpdateDrops[lineage] = ownedRecord
					}
					matchedVisibleKeys[visibleRecordKey(hostnameMatches[0].Hostname, hostnameMatches[0].Answer)] = struct{}{}
					pl.Updates = append(pl.Updates, reconcileUpdateCall{From: hostnameMatches[0], To: d})
					continue
				}
				continue
			}
			if len(visibleByKey[newKey]) == 0 {
				if len(hostnameMatches) > 0 {
					continue
				}
				hostname := normalizeHostname(d.Hostname)
				if _, planned := plannedCreateHostnames[hostname]; planned {
					continue
				}
				if ownedLineage != lineage {
					pl.CreateDrops[lineage] = ownedRecord
				}
				pl.Creates = append(pl.Creates, d)
				plannedCreateHostnames[hostname] = struct{}{}
				continue
			}
			if visibleByKey[newKey][0].Proxied != d.Proxied && canUpdateOwnedVisibleRecord(owned.InstallationID, ownedRecord, visibleByKey[newKey][0], matchedByHostnameOnly) {
				if ownedLineage != lineage {
					pl.UpdateDrops[lineage] = ownedRecord
				}
				matchedVisibleKeys[newKey] = struct{}{}
				pl.Updates = append(pl.Updates, reconcileUpdateCall{From: visibleByKey[newKey][0], To: d})
				continue
			}
			provenance := copyRecordProvenance(ownedRecord.Provenance)
			if hasCurrentRecordOwnership(owned.InstallationID, ownedRecord, visibleByKey[newKey][0]) {
				provenance = copyRecordProvenance(visibleByKey[newKey][0].Provenance)
			}
			if shouldTagLegacyRecord(owned.InstallationID, ownedRecord, visibleByKey[newKey][0]) {
				matchedVisibleKeys[newKey] = struct{}{}
				pl.Updates = append(pl.Updates, reconcileUpdateCall{From: visibleByKey[newKey][0], To: d})
				continue
			}
			if ownedLineage != lineage {
				pl.KeepDrops[lineage] = ownedRecord
			}
			matchedVisibleKeys[newKey] = struct{}{}
			pl.NextManaged = append(pl.NextManaged, state.ManagedRecord{
				Output:     output,
				Source:     d.Source,
				Hostname:   d.Hostname,
				Answer:     d.Answer,
				Proxied:    d.Proxied,
				Provenance: provenance,
			})
			continue
		}

		key := visibleRecordKey(d.Hostname, d.Answer)
		hostname := normalizeHostname(d.Hostname)
		if len(ownedByHostname[hostname]) == 0 && len(visibleByKey[key]) == 1 && len(visibleByHostname[hostname]) == 1 && hasCurrentInstallationMarker(visibleByKey[key][0].Provenance, owned.InstallationID) {
			if _, matched := matchedVisibleKeys[key]; !matched {
				pl.NextManaged = append(pl.NextManaged, state.ManagedRecord{
					Output:     output,
					Source:     d.Source,
					Hostname:   d.Hostname,
					Answer:     d.Answer,
					Proxied:    d.Proxied,
					Provenance: copyRecordProvenance(visibleByKey[key][0].Provenance),
				})
				matchedVisibleKeys[key] = struct{}{}
				continue
			}
		}
		if len(visibleByHostname[hostname]) == 1 && len(visibleByKey[key]) == 0 && hasCurrentInstallationMarker(visibleByHostname[hostname][0].Provenance, owned.InstallationID) {
			visibleRecord := visibleByHostname[hostname][0]
			visibleKey := visibleRecordKey(visibleRecord.Hostname, visibleRecord.Answer)
			if _, matched := matchedVisibleKeys[visibleKey]; !matched {
				pl.Updates = append(pl.Updates, reconcileUpdateCall{From: visibleRecord, To: d})
				matchedVisibleKeys[visibleKey] = struct{}{}
				continue
			}
		}
		if len(visibleByKey[key]) == 0 && len(visibleByHostname[hostname]) == 0 {
			if _, planned := plannedCreateHostnames[hostname]; planned {
				continue
			}
			pl.Creates = append(pl.Creates, d)
			plannedCreateHostnames[hostname] = struct{}{}
		}
	}

	for lineage, m := range ownedByLineage {
		if _, matched := matchedOwnedLineages[lineage]; matched {
			continue
		}
		if _, keep := desiredByLineage[lineage]; keep {
			continue
		}
		if grace > 0 && (m.RemoveAfter == nil || m.RemoveAfter.After(now)) {
			if m.RemoveAfter == nil {
				deadline := now.Add(grace)
				m.RemoveAfter = &deadline
			}
			pl.NextManaged = append(pl.NextManaged, m)
			continue
		}
		if visible, ok := staleOwnedDeleteVisibleRecord(owned.InstallationID, m, visibleByKey, visibleByHostname); ok {
			pl.Deletes = append(pl.Deletes, reconcileDeleteCall{Visible: visible, Managed: m})
			continue
		}
		if shouldRetainManagedRecordWithoutProof(m, visibleByHostname) {
			// The record remains owned for safety, but this cleanup cannot be
			// proven. Do not retain an already-expired deadline: doing so would
			// schedule immediate reconciles forever.
			m.RemoveAfter = nil
			pl.NextManaged = append(pl.NextManaged, m)
			pl.Blocked = append(pl.Blocked, m)
			continue
		}
		pl.Drops = append(pl.Drops, m)
	}

	for key, records := range visibleByKey {
		if len(records) != 1 {
			continue
		}
		visible := records[0]
		if _, matched := matchedVisibleKeys[key]; matched || len(ownedByHostname[normalizeHostname(visible.Hostname)]) != 0 || len(visibleByHostname[normalizeHostname(visible.Hostname)]) != 1 || !hasCurrentInstallationMarker(visible.Provenance, owned.InstallationID) {
			continue
		}
		pl.Deletes = append(pl.Deletes, reconcileDeleteCall{
			Visible: visible,
			Managed: state.ManagedRecord{Output: output, Hostname: visible.Hostname, Answer: visible.Answer, Provenance: copyRecordProvenance(visible.Provenance)},
		})
	}

	sort.Slice(pl.Creates, func(i, j int) bool {
		return visibleRecordKey(pl.Creates[i].Hostname, pl.Creates[i].Answer) < visibleRecordKey(pl.Creates[j].Hostname, pl.Creates[j].Answer)
	})
	sort.Slice(pl.Updates, func(i, j int) bool {
		return visibleRecordKey(pl.Updates[i].To.Hostname, pl.Updates[i].To.Answer) < visibleRecordKey(pl.Updates[j].To.Hostname, pl.Updates[j].To.Answer)
	})
	sort.Slice(pl.Deletes, func(i, j int) bool {
		return visibleRecordKey(pl.Deletes[i].Visible.Hostname, pl.Deletes[i].Visible.Answer) < visibleRecordKey(pl.Deletes[j].Visible.Hostname, pl.Deletes[j].Visible.Answer)
	})

	return pl
}

func staleOwnedDeleteVisibleRecord(installationID string, managed state.ManagedRecord, visibleByKey, visibleByHostname map[string][]contracts.VisibleRecord) (contracts.VisibleRecord, bool) {
	key := visibleRecordKey(managed.Hostname, managed.Answer)
	matches := visibleByKey[key]
	if len(matches) == 1 {
		visible := matches[0]
		if hasCurrentRecordOwnership(installationID, managed, visible) {
			return visible, true
		}
	}
	if len(matches) == 0 {
		hostnameMatches := visibleByHostname[normalizeHostname(managed.Hostname)]
		if len(hostnameMatches) == 1 && hasCurrentRecordOwnership(installationID, managed, hostnameMatches[0]) {
			return hostnameMatches[0], true
		}
	}

	return contracts.VisibleRecord{}, false
}

func shouldRetainManagedRecordWithoutProof(managed state.ManagedRecord, visibleByHostname map[string][]contracts.VisibleRecord) bool {
	return len(visibleByHostname[normalizeHostname(managed.Hostname)]) > 0
}

func canUpdateOwnedVisibleRecord(installationID string, owned state.ManagedRecord, visible contracts.VisibleRecord, matchedByHostnameOnly bool) bool {
	if hasInstallationMarker(visible.Provenance) {
		return hasCurrentInstallationMarker(visible.Provenance, installationID) && sameRecordProvenance(owned.Provenance, visible.Provenance)
	}
	if hasRecordProvenance(visible.Provenance) {
		return !hasInstallationMarker(owned.Provenance) && sameRecordProvenance(owned.Provenance, visible.Provenance)
	}
	return !matchedByHostnameOnly && !hasRecordProvenance(owned.Provenance) && !hasInstallationMarker(owned.Provenance)
}

func hasCurrentInstallationMarker(provenance *contracts.RecordProvenance, installationID string) bool {
	return hasRecordProvenance(provenance) && hasInstallationMarker(provenance) && strings.EqualFold(strings.TrimSpace(provenance.InstallationID), strings.TrimSpace(installationID))
}

func hasCurrentRecordOwnership(installationID string, managed state.ManagedRecord, visible contracts.VisibleRecord) bool {
	return hasCurrentInstallationMarker(visible.Provenance, installationID) && sameRecordProvenance(managed.Provenance, visible.Provenance)
}

func shouldTagLegacyRecord(installationID string, managed state.ManagedRecord, visible contracts.VisibleRecord) bool {
	return strings.TrimSpace(installationID) != "" && hasRecordProvenance(managed.Provenance) && hasRecordProvenance(visible.Provenance) && !hasInstallationMarker(managed.Provenance) && !hasInstallationMarker(visible.Provenance) && sameRecordProvenance(managed.Provenance, visible.Provenance)
}

func hasInstallationMarker(provenance *contracts.RecordProvenance) bool {
	return provenance != nil && strings.TrimSpace(provenance.InstallationID) != ""
}

func hasRecordProvenance(provenance *contracts.RecordProvenance) bool {
	return provenance != nil && provenance.RemoteID != ""
}

func sameRecordProvenance(managed, visible *contracts.RecordProvenance) bool {
	if managed == nil || visible == nil {
		return false
	}
	if managed.RemoteID == "" || visible.RemoteID == "" {
		return false
	}

	return managed.RemoteID == visible.RemoteID
}

func nextManagedProvenance(primary, fallback *contracts.RecordProvenance) *contracts.RecordProvenance {
	if primary != nil {
		copy := *primary
		return &copy
	}
	if fallback != nil {
		copy := *fallback
		return &copy
	}

	return nil
}
