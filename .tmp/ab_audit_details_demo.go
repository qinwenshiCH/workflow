package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Row struct {
	ID        int
	FFKey     string
	Name      string
	Status    string
	Enabled   bool
	OnlineAt  *time.Time
	OfflineAt *time.Time
	Details   string
}

type FFRule struct {
	ID       string      `json:"id"`
	Name     string      `json:"name,omitempty"`
	Rollout  float64     `json:"rollout,omitempty"`
	Override *int        `json:"override,omitempty"`
	Variants []FFVariant `json:"variants,omitempty"`
}

type FFVariant struct {
	ID         int      `json:"id"`
	VariantKey string   `json:"variant_key"`
	Threshold  *float64 `json:"threshold,omitempty"`
	Payload    string   `json:"payload,omitempty"`
	Default    bool     `json:"default,omitempty"`
	Control    bool     `json:"control,omitempty"`
}

type FFParam struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type FFMetricID struct {
	ID int `json:"id"`
}

type FFOperationRecord struct {
	OperationType string    `json:"operation_type"`
	OperateBy     int64     `json:"operate_by"`
	OperateAt     time.Time `json:"operate_at"`
}

type FFExpSetting struct {
	Desc           string  `json:"desc,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	TargetDuration int     `json:"target_duration,omitempty"`
}

type FFExpDetails struct {
	MaxVariantID     int                 `json:"max_variant_id"`
	ExpSetting       FFExpSetting        `json:"exp_setting"`
	ReleasePlan      []FFRule            `json:"release_plan,omitempty"`
	Override         []FFRule            `json:"override"`
	Gates            []FFRule            `json:"gates"`
	Variants         []FFVariant         `json:"variants"`
	Params           []FFParam           `json:"params"`
	Metrics          []FFMetricID        `json:"metrics"`
	OperationRecords []FFOperationRecord `json:"operation_records"`
	FfReportJobID    *int64              `json:"ff_report_job_id,omitempty"`
}

type Change struct {
	Field  string
	Action string
	Before any
	After  any
}

func main() {
	t0 := mustTime("2026-07-02T10:00:00Z")
	t1 := mustTime("2026-07-02T10:05:00Z")
	base := Row{
		ID:      101,
		FFKey:   "checkout_exp",
		Name:    "Checkout Experiment",
		Status:  "DRAFT",
		Enabled: false,
		Details: mustJSON(FFExpDetails{
			MaxVariantID: 2,
			ExpSetting: FFExpSetting{
				Desc:           "baseline",
				Confidence:     0.95,
				TargetDuration: 14,
			},
			Override: []FFRule{},
			Gates:    []FFRule{},
			Variants: []FFVariant{
				{ID: 1, VariantKey: "control", Threshold: floatPtr(50), Control: true},
				{ID: 2, VariantKey: "treatment", Threshold: floatPtr(50)},
			},
			Params:      []FFParam{{Name: "btn_color", Type: "string"}},
			Metrics:     []FFMetricID{{ID: 11}},
			ReleasePlan: []FFRule{},
			OperationRecords: []FFOperationRecord{{
				OperationType: "CREATE",
				OperateBy:     9001,
				OperateAt:     t0,
			}},
		}),
	}

	updated := base
	updated.Name = "Checkout Experiment V2"
	updated.Details = mustJSON(FFExpDetails{
		MaxVariantID: 2,
		ExpSetting: FFExpSetting{
			Desc:           "new checkout copy with hero text",
			Confidence:     0.95,
			TargetDuration: 14,
		},
		Override: []FFRule{},
		Gates:    []FFRule{},
		Variants: []FFVariant{
			{ID: 1, VariantKey: "control", Threshold: floatPtr(40), Control: true},
			{ID: 2, VariantKey: "treatment", Threshold: floatPtr(60)},
		},
		Params:      []FFParam{{Name: "btn_color", Type: "string"}},
		Metrics:     []FFMetricID{{ID: 11}},
		ReleasePlan: []FFRule{},
		OperationRecords: []FFOperationRecord{{
			OperationType: "CREATE",
			OperateBy:     9001,
			OperateAt:     t0,
		}},
	})

	copied := updated
	copied.ID = 202
	copied.Name = "Checkout Experiment V2 (copy 20260702100500)"
	copied.Details = mustJSON(FFExpDetails{
		MaxVariantID: 2,
		ExpSetting: FFExpSetting{
			Desc:           "new checkout copy with hero text",
			Confidence:     0.95,
			TargetDuration: 14,
		},
		Override: []FFRule{},
		Gates:    []FFRule{},
		Variants: []FFVariant{
			{ID: 1, VariantKey: "control", Threshold: floatPtr(40), Control: true},
			{ID: 2, VariantKey: "treatment", Threshold: floatPtr(60)},
		},
		Params:      []FFParam{{Name: "btn_color", Type: "string"}},
		Metrics:     []FFMetricID{{ID: 11}},
		ReleasePlan: []FFRule{},
		OperationRecords: []FFOperationRecord{{
			OperationType: "COPY",
			OperateBy:     9002,
			OperateAt:     t1,
		}},
	})

	online := updated
	online.Status = "RUNNING"
	online.Enabled = true
	online.OnlineAt = &t1
	online.Details = mustJSON(FFExpDetails{
		MaxVariantID: 2,
		ExpSetting: FFExpSetting{
			Desc:           "new checkout copy with hero text",
			Confidence:     0.95,
			TargetDuration: 14,
		},
		Override: []FFRule{},
		Gates:    []FFRule{},
		Variants: []FFVariant{
			{ID: 1, VariantKey: "control", Threshold: floatPtr(40), Control: true},
			{ID: 2, VariantKey: "treatment", Threshold: floatPtr(60)},
		},
		Params:      []FFParam{{Name: "btn_color", Type: "string"}},
		Metrics:     []FFMetricID{{ID: 11}},
		ReleasePlan: []FFRule{},
		OperationRecords: []FFOperationRecord{
			{OperationType: "CREATE", OperateBy: 9001, OperateAt: t0},
			{OperationType: "ONLINE", OperateBy: 9002, OperateAt: t1},
		},
	})

	onlineScheduler := online
	jobID := int64(777)
	onlineScheduler.Details = mustJSON(FFExpDetails{
		MaxVariantID: 2,
		ExpSetting: FFExpSetting{
			Desc:           "new checkout copy with hero text",
			Confidence:     0.95,
			TargetDuration: 14,
		},
		Override: []FFRule{},
		Gates:    []FFRule{},
		Variants: []FFVariant{
			{ID: 1, VariantKey: "control", Threshold: floatPtr(40), Control: true},
			{ID: 2, VariantKey: "treatment", Threshold: floatPtr(60)},
		},
		Params:      []FFParam{{Name: "btn_color", Type: "string"}},
		Metrics:     []FFMetricID{{ID: 11}},
		ReleasePlan: []FFRule{},
		OperationRecords: []FFOperationRecord{
			{OperationType: "CREATE", OperateBy: 9001, OperateAt: t0},
			{OperationType: "ONLINE", OperateBy: 9002, OperateAt: t1},
		},
		FfReportJobID: &jobID,
	})

	fmt.Println("=== AB details-aware audit demo ===")
	fmt.Println()

	printScenario("business update", base, updated)
	printCreateScenario("copy create", copied)
	printScenario("online status update", updated, online)
	printScenario("scheduler side effect after online", online, onlineScheduler)
}

func printCreateScenario(title string, after Row) {
	fmt.Printf("[%s]\n", title)
	fmt.Printf("generic row-level callback action: created\n")
	fmt.Printf("details-aware classifier action: %s\n", classifyCreate(after))
	fmt.Printf("projected changes:\n")
	for _, c := range diffProjected(nil, &after) {
		fmt.Printf("  - %s: %v -> %v\n", c.Field, c.Before, c.After)
	}
	fmt.Println()
}

func printScenario(title string, before Row, after Row) {
	fmt.Printf("[%s]\n", title)
	fmt.Printf("latest operation in details: %s\n", latestOperation(after))
	changes := diffProjected(&before, &after)
	if shouldSkipABEvent(&before, &after, changes) {
		fmt.Printf("details-aware decision: skip audit event\n\n")
		return
	}
	fmt.Printf("details-aware decision: write action=updated with %d change(s)\n", len(changes))
	for _, c := range changes {
		fmt.Printf("  - %s: %v -> %v\n", c.Field, c.Before, c.After)
	}
	fmt.Println()
}

func classifyCreate(after Row) string {
	if latestOperation(after) == "COPY" {
		return "copied"
	}
	return "created"
}

func shouldSkipABEvent(before *Row, after *Row, changes []Change) bool {
	switch latestOperation(*after) {
	case "DEBUG", "ONLINE", "OFFLINE", "RELEASE":
		return true
	}
	return len(changes) == 0
}

func diffProjected(before *Row, after *Row) []Change {
	var changes []Change
	beforeMap := projectAB(before)
	afterMap := projectAB(after)

	keysMap := map[string]bool{}
	for k := range beforeMap {
		keysMap[k] = true
	}
	for k := range afterMap {
		keysMap[k] = true
	}
	keys := make([]string, 0, len(keysMap))
	for k := range keysMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		b, bok := beforeMap[key]
		a, aok := afterMap[key]
		switch {
		case !bok && aok:
			changes = append(changes, Change{Field: key, Action: "created", After: a})
		case bok && !aok:
			changes = append(changes, Change{Field: key, Action: "deleted", Before: b})
		case bok && aok && !jsonEqual(b, a):
			changes = append(changes, Change{Field: key, Action: "changed", Before: b, After: a})
		}
	}
	return changes
}

func projectAB(row *Row) map[string]any {
	if row == nil {
		return map[string]any{}
	}

	var details FFExpDetails
	if err := json.Unmarshal([]byte(row.Details), &details); err != nil {
		panic(err)
	}

	result := map[string]any{
		"name":                         row.Name,
		"details.exp_setting.desc":     details.ExpSetting.Desc,
		"details.exp_setting.conf":     details.ExpSetting.Confidence,
		"details.exp_setting.duration": details.ExpSetting.TargetDuration,
		"details.variants":             stableJSON(details.Variants),
		"details.params":               stableJSON(details.Params),
		"details.metrics":              stableJSON(details.Metrics),
		"details.override":             stableJSON(details.Override),
		"details.gates":                stableJSON(details.Gates),
		"details.release_plan":         stableJSON(details.ReleasePlan),
	}

	// Intentionally ignore:
	// - row.Status / row.Enabled / OnlineAt / OfflineAt
	// - details.OperationRecords
	// - details.FfReportJobID
	// These are exactly the places where generic callback diff is noisy in Wave AB.
	return result
}

func latestOperation(row Row) string {
	var details FFExpDetails
	if err := json.Unmarshal([]byte(row.Details), &details); err != nil {
		panic(err)
	}
	if len(details.OperationRecords) == 0 {
		return ""
	}
	return details.OperationRecords[len(details.OperationRecords)-1].OperationType
}

func stableJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func jsonEqual(a any, b any) bool {
	return stableJSON(a) == stableJSON(b)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func mustTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		panic(err)
	}
	return t
}

func floatPtr(v float64) *float64 {
	return &v
}

func init() {
	// Keep output deterministic if a formatter reorders imports and removes unused ones.
	_ = strings.Builder{}
}
