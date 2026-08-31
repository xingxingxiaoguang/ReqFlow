package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// 这些内存仓储供仍在使用的归档、数据集和元数据用例测试共享；它们不模拟已删除的
// 旧任务运行器，只镜像仓储合同。
type memTasks struct {
	mu    sync.Mutex
	tasks []model.Task
	steps map[string][]model.TaskStep
	items map[string][]model.TaskItem
	seq   int
}

func newMemTasks() *memTasks {
	return &memTasks{steps: map[string][]model.TaskStep{}, items: map[string][]model.TaskItem{}}
}

func (r *memTasks) CreateTask(_ context.Context, task *model.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	task.ID = fmt.Sprintf("task-%d", r.seq)
	task.CreatedAt, task.UpdatedAt = time.Now(), time.Now()
	r.tasks = append(r.tasks, *task)
	return nil
}

func (r *memTasks) UpdateTask(_ context.Context, task *model.Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.tasks {
		if r.tasks[i].ID == task.ID {
			task.UpdatedAt = time.Now()
			r.tasks[i] = *task
			return nil
		}
	}
	return fmt.Errorf("任务不存在")
}

func (r *memTasks) ListTasks(_ context.Context, filter port.TaskFilter) ([]model.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []model.Task
	for i := len(r.tasks) - 1; i >= 0; i-- {
		task := r.tasks[i]
		if filter.Status != "" && task.Status != filter.Status || filter.Type != "" && task.Type != filter.Type {
			continue
		}
		result = append(result, task)
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (r *memTasks) CountTasks(context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.tasks)), nil
}

func (r *memTasks) GetTask(_ context.Context, id string) (*model.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.tasks {
		if r.tasks[i].ID == id {
			copy := r.tasks[i]
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("任务不存在")
}

func (r *memTasks) CreateTaskSteps(_ context.Context, taskID string, steps []model.TaskStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range steps {
		r.seq++
		steps[i].ID, steps[i].TaskID = fmt.Sprintf("step-%d", r.seq), taskID
	}
	r.steps[taskID] = append([]model.TaskStep(nil), steps...)
	return nil
}

func (r *memTasks) GetTaskSteps(_ context.Context, taskID string) ([]model.TaskStep, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.TaskStep(nil), r.steps[taskID]...), nil
}

func (r *memTasks) UpdateTaskStep(_ context.Context, step *model.TaskStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.steps[step.TaskID] {
		if r.steps[step.TaskID][i].ID == step.ID {
			r.steps[step.TaskID][i] = *step
			return nil
		}
	}
	return fmt.Errorf("步骤不存在")
}

func (r *memTasks) GetTaskItems(_ context.Context, taskID string) ([]model.TaskItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.TaskItem(nil), r.items[taskID]...), nil
}

func (r *memTasks) ReplaceTaskItems(_ context.Context, taskID string, items []model.TaskItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := make([]model.TaskItem, 0, len(items))
	for _, item := range r.items[taskID] {
		if item.Status == model.ItemStatusSuccess {
			kept = append(kept, item)
		}
	}
	for i := range items {
		r.seq++
		if items[i].ID == "" {
			items[i].ID = fmt.Sprintf("item-%d", r.seq)
		}
		items[i].TaskID = taskID
		kept = append(kept, items[i])
	}
	r.items[taskID] = kept
	return nil
}

func (r *memTasks) UpdateItemResult(_ context.Context, itemID, status, errMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for taskID, items := range r.items {
		for i := range items {
			if items[i].ID == itemID {
				items[i].Status, items[i].ErrorMessage = status, errMessage
				r.items[taskID] = items
				return nil
			}
		}
	}
	return fmt.Errorf("明细不存在")
}

func (r *memTasks) RecoverStuck(_ context.Context) error { return nil }

type memDatasets struct {
	mu       sync.Mutex
	datasets []model.Dataset
	items    map[string][]model.DatasetItem
	seq      int
}

func newMemDatasets() *memDatasets { return &memDatasets{items: map[string][]model.DatasetItem{}} }

func (r *memDatasets) CreateDataset(_ context.Context, dataset *model.Dataset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if dataset.ID == "" {
		r.seq++
		dataset.ID = fmt.Sprintf("ds-%d", r.seq)
	}
	dataset.CreatedAt = time.Now()
	r.datasets = append(r.datasets, *dataset)
	return nil
}

func (r *memDatasets) UpdateDataset(_ context.Context, dataset *model.Dataset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.datasets {
		if r.datasets[i].ID == dataset.ID {
			r.datasets[i] = *dataset
			return nil
		}
	}
	return fmt.Errorf("数据集不存在")
}

func (r *memDatasets) ListDatasets(_ context.Context, typ string, _ int) ([]model.Dataset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []model.Dataset
	for _, dataset := range r.datasets {
		if typ == "" || dataset.Type == typ {
			result = append(result, dataset)
		}
	}
	return result, nil
}

func (r *memDatasets) GetDataset(_ context.Context, id string) (*model.Dataset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.datasets {
		if r.datasets[i].ID == id {
			copy := r.datasets[i]
			return &copy, nil
		}
	}
	return nil, fmt.Errorf("数据集不存在")
}

func (r *memDatasets) CountDatasets(context.Context, string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.datasets)), nil
}

func (*memDatasets) CountDatasetItems(context.Context, string) (int64, error) { return 0, nil }

func (r *memDatasets) ReplaceDatasetItems(_ context.Context, id string, items []port.DatasetItemVector) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]model.DatasetItem, len(items))
	for i, item := range items {
		result[i] = model.DatasetItem{ID: item.ID, DatasetID: id, Fields: item.Fields}
	}
	r.items[id] = result
	return nil
}

func (r *memDatasets) ListDatasetItemsByType(context.Context, string) ([]model.DatasetItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []model.DatasetItem
	for _, items := range r.items {
		result = append(result, items...)
	}
	return result, nil
}

func (r *memDatasets) ListDatasetItems(_ context.Context, id string, _ int) ([]model.DatasetItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.DatasetItem(nil), r.items[id]...), nil
}

func (*memDatasets) SearchSimilarDatasetItems(context.Context, []float32, string, int) ([]port.SimilarDatasetItem, error) {
	return nil, nil
}

func (r *memDatasets) UpsertDatasetItems(_ context.Context, datasetID, sourceTaskID string,
	items []port.DatasetItemVector, mode port.UpsertMode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.items[datasetID]
	for _, item := range items {
		index := -1
		for i := range list {
			if list[i].ItemKey != "" && list[i].ItemKey == item.ItemKey {
				index = i
				break
			}
		}
		if index < 0 {
			r.seq++
			list = append(list, model.DatasetItem{ID: fmt.Sprintf("di-%d", r.seq), DatasetID: datasetID,
				Fields: item.Fields, ItemKey: item.ItemKey, Fingerprint: item.Fingerprint, SourceTaskID: sourceTaskID})
		} else if mode == port.UpsertUpdateExisting && list[index].Fingerprint != item.Fingerprint {
			list[index].Fields, list[index].Fingerprint, list[index].SourceTaskID = item.Fields, item.Fingerprint, sourceTaskID
		}
	}
	r.items[datasetID] = list
	return nil
}

func (r *memDatasets) DeleteDatasetItemsBySource(_ context.Context, datasetID, sourceTaskID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kept []model.DatasetItem
	removed := int64(0)
	for _, item := range r.items[datasetID] {
		if item.SourceTaskID == sourceTaskID {
			removed++
		} else {
			kept = append(kept, item)
		}
	}
	r.items[datasetID] = kept
	return removed, nil
}

func (r *memDatasets) GetDatasetItemKeyMap(_ context.Context, datasetID string) (map[string]port.DatasetItemKeyInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := map[string]port.DatasetItemKeyInfo{}
	for _, item := range r.items[datasetID] {
		if item.ItemKey != "" {
			result[item.ItemKey] = port.DatasetItemKeyInfo{ID: item.ID, Fingerprint: item.Fingerprint, SourceTaskID: item.SourceTaskID}
		}
	}
	return result, nil
}

func (r *memDatasets) CountDatasetItemsOfDataset(_ context.Context, datasetID string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.items[datasetID])), nil
}

func (r *memDatasets) ListDatasetItemsFiltered(_ context.Context, filter port.ItemFilter) ([]model.DatasetItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []model.DatasetItem
	for _, items := range r.items {
		for _, item := range items {
			if filter.DatasetID == "" || item.DatasetID == filter.DatasetID {
				result = append(result, item)
			}
		}
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func (*memDatasets) SearchSimilarDatasetItemsFiltered(context.Context, []float32, port.ItemFilter, int) ([]port.SimilarDatasetItem, error) {
	return nil, nil
}

func (r *memDatasets) UpdateDatasetSchema(_ context.Context, datasetID, payload string, version int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.datasets {
		if r.datasets[i].ID == datasetID {
			r.datasets[i].Schema, r.datasets[i].SchemaVersion = payload, version
			return nil
		}
	}
	return fmt.Errorf("数据集不存在")
}

func (*memDatasets) SearchDatasetItemsFTS(context.Context, string, []string, string, int) ([]model.DatasetItem, error) {
	return nil, nil
}
