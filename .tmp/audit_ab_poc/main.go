package main

import (
	"context"
	"fmt"
	"log"
	"time"

	abdao "wave/apps/web/dao/ab"
	globaldao "wave/apps/web/dao/global"
	"wave/pkg/lib/pvctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	db, err := gorm.Open(sqlite.Open("file:audit_ab_poc?mode=memory&cache=shared&_loc=UTC"), &gorm.Config{})
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}

	mustExec(db, `CREATE TABLE ab_feature_flag (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ffkey TEXT,
		name TEXT,
		typ INTEGER,
		enabled BOOLEAN,
		status TEXT,
		details TEXT,
		bucket_bits INTEGER DEFAULT 0,
		is_deleted BOOLEAN DEFAULT 0,
		created_by INTEGER,
		updated_by INTEGER,
		created_at DATETIME,
		updated_at DATETIME
	)`)

	mustExec(db, `CREATE TABLE project_member (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER,
		account_id INTEGER,
		role_ids TEXT,
		is_deleted BOOLEAN DEFAULT 0,
		created_by INTEGER,
		updated_by INTEGER,
		created_at DATETIME,
		updated_at DATETIME
	)`)

	registerCallbacks(db)

	ctx := pvctx.WithPid(pvctx.WithAid(context.Background(), 2001), 3001)
	now := time.Date(2026, 7, 2, 16, 0, 0, 0, time.UTC)

	fmt.Println("== AB create ==")
	orig := abdao.AbFeatureFlag{
		FFKey:     "pricing_exp",
		Name:      "Pricing Experiment",
		Typ:       3,
		Status:    "DRAFT",
		CreatedBy: 2001,
		UpdatedBy: 2001,
		CreatedAt: now,
		UpdatedAt: now,
	}
	must(db.WithContext(ctx).Create(&orig).Error)

	fmt.Println("== AB copy (still looks like create to callback) ==")
	copyRec := abdao.AbFeatureFlag{
		FFKey:     "pricing_exp_copy",
		Name:      "Pricing Experiment Copy",
		Typ:       3,
		Status:    "DRAFT",
		CreatedBy: 2001,
		UpdatedBy: 2001,
		CreatedAt: now,
		UpdatedAt: now,
	}
	must(db.WithContext(ctx).Create(&copyRec).Error)

	fmt.Println("== AB status update ==")
	must(db.WithContext(ctx).
		Model(&abdao.AbFeatureFlag{}).
		Where("id = ?", orig.ID).
		Updates(abdao.AbFeatureFlag{
			Enabled:   true,
			Status:    "RUNNING",
			UpdatedBy: 2001,
			UpdatedAt: now.Add(time.Minute),
		}).Error)

	fmt.Println("== AB internal follow-up update in same request ctx ==")
	detail := `{"ff_report_job_id":123}`
	must(db.WithContext(ctx).
		Model(&abdao.AbFeatureFlag{}).
		Where("id = ?", orig.ID).
		Updates(abdao.AbFeatureFlag{
			Details:   &detail,
			UpdatedBy: 2001,
			UpdatedAt: now.Add(2 * time.Minute),
		}).Error)

	fmt.Println("== ProjectMember batch create ==")
	members := []*globaldao.ProjectMember{
		{
			ProjectId: 3001,
			AccountId: 4001,
			CreatedBy: 2001,
			UpdatedBy: 2001,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ProjectId: 3001,
			AccountId: 4002,
			CreatedBy: 2001,
			UpdatedBy: 2001,
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	must(db.WithContext(ctx).Create(members).Error)
}

func registerCallbacks(db *gorm.DB) {
	must(db.Callback().Create().After("gorm:after_create").Register("poc:audit:create", func(tx *gorm.DB) {
		printRecord("CREATE", tx)
	}))
	must(db.Callback().Update().After("gorm:after_update").Register("poc:audit:update", func(tx *gorm.DB) {
		printRecord("UPDATE", tx)
	}))
}

func printRecord(op string, tx *gorm.DB) {
	table := tx.Statement.Table
	if tx.Statement.Schema != nil {
		table = tx.Statement.Schema.Table
	}

	kind := "<invalid>"
	if tx.Statement.ReflectValue.IsValid() {
		kind = tx.Statement.ReflectValue.Kind().String()
	}

	aid := pvctx.Aid(tx.Statement.Context)
	pid := pvctx.Pid(tx.Statement.Context)

	if table == "ab_feature_flag" && tx.Statement.ReflectValue.IsValid() && tx.Statement.ReflectValue.Kind() == 0 {
		fmt.Printf("%s table=%s kind=%s aid=%d pid=%d\n", op, table, kind, aid, pid)
		return
	}

	switch table {
	case "ab_feature_flag":
		ff, ok := tx.Statement.Dest.(*abdao.AbFeatureFlag)
		if ok {
			fmt.Printf("%s table=%s kind=%s aid=%d pid=%d ffkey=%s name=%s status=%s\n",
				op, table, kind, aid, pid, ff.FFKey, ff.Name, ff.Status)
			return
		}
	case "project_member":
		fmt.Printf("%s table=%s kind=%s aid=%d pid=%d rows=%d\n",
			op, table, kind, aid, pid, tx.RowsAffected)
		return
	}

	fmt.Printf("%s table=%s kind=%s aid=%d pid=%d\n", op, table, kind, aid, pid)
}

func mustExec(db *gorm.DB, sql string) {
	must(db.Exec(sql).Error)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
