package pipeline

import (
	"context"
	"errors"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type schemaDeleteRepo struct {
	port.DatasetPipelineRepo
	schema  *model.DatasetSchemaDefinition
	usage   [3]int // datasets, extractionProfiles, retrievalProfiles
	deleted bool
}

func (r *schemaDeleteRepo) GetDatasetSchema(_ context.Context, id string) (*model.DatasetSchemaDefinition, error) {
	if r.schema == nil || r.schema.ID != id {
		return nil, errors.New("not found")
	}
	clone := *r.schema
	return &clone, nil
}

func (r *schemaDeleteRepo) CountDatasetSchemaUsage(_ context.Context, _ string) (int, int, int, error) {
	return r.usage[0], r.usage[1], r.usage[2], nil
}

func (r *schemaDeleteRepo) DeleteDatasetSchema(_ context.Context, id string) (bool, error) {
	if r.schema == nil || r.schema.ID != id {
		return false, nil
	}
	r.deleted = true
	return true, nil
}

func TestDeleteSchemaGuardsUsage(t *testing.T) {
	ctx := context.Background()
	schema := &model.DatasetSchemaDefinition{ID: "schema-1", Name: "s"}
	repo := &schemaDeleteRepo{schema: schema}
	service := &DatasetService{repo: repo}

	// 任一引用存在都拒绝删除
	for i := 0; i < 3; i++ {
		repo.usage = [3]int{}
		repo.usage[i] = 2
		if _, err := service.DeleteSchema(ctx, schema.ID); !errors.Is(err, ErrSchemaInUse) {
			t.Fatalf("case %d: want ErrSchemaInUse, got %v", i, err)
		}
		if repo.deleted {
			t.Fatalf("case %d: schema must not be deleted while in use", i)
		}
	}

	repo.usage = [3]int{}
	deleted, err := service.DeleteSchema(ctx, schema.ID)
	if err != nil || !deleted {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
	if !repo.deleted {
		t.Fatal("unused schema should be deleted")
	}

	if _, err := service.DeleteSchema(ctx, "missing"); err == nil {
		t.Fatal("deleting a missing schema should fail")
	}
}
