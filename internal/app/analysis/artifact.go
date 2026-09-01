package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type ArtifactService struct {
	repo  port.AnalysisRepo
	blobs port.BlobStore
}

func NewArtifactService(repo port.AnalysisRepo, blobs port.BlobStore) (*ArtifactService, error) {
	if repo == nil || blobs == nil {
		return nil, fmt.Errorf("artifact service 依赖不完整")
	}
	return &ArtifactService{repo: repo, blobs: blobs}, nil
}

type PublishArtifactInput struct {
	WorkspaceID           string
	Kind                  string
	Name                  string
	Content               []byte
	ProducerWorkflowRunID string
	ProducerNodeRunID     string
	ProducerAttempt       int
	Metadata              map[string]any
}

func (s *ArtifactService) Publish(ctx context.Context, input PublishArtifactInput) (*model.Artifact, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" {
		input.WorkspaceID = "default"
	}
	input.Name, input.Kind = strings.TrimSpace(input.Name), strings.TrimSpace(input.Kind)
	if input.Name == "" || len(input.Name) > 240 {
		return nil, fmt.Errorf("Artifact 名称必须为 1..240 字节")
	}
	switch input.Kind {
	case model.ArtifactMarkdown, model.ArtifactJSON, model.ArtifactGraphManifest:
	default:
		return nil, fmt.Errorf("当前 artifact.render 只支持 markdown、json、graph_manifest")
	}
	if len(input.Content) == 0 || len(input.Content) > 16<<20 {
		return nil, fmt.Errorf("Artifact 内容必须为 1..16MiB")
	}
	object, err := s.blobs.Put(ctx, bytes.NewReader(input.Content))
	if err != nil {
		return nil, err
	}
	metadata, _ := json.Marshal(input.Metadata)
	return s.repo.CreateArtifactForNode(ctx, &model.Artifact{WorkspaceID: input.WorkspaceID,
		Kind: input.Kind, Name: input.Name, BlobURI: object.URI, ContentHash: object.SHA256,
		ProducerWorkflowRunID: input.ProducerWorkflowRunID, ProducerNodeRunID: input.ProducerNodeRunID,
		ProducerAttempt: input.ProducerAttempt, Metadata: string(metadata)}, input.ProducerAttempt)
}

func (s *ArtifactService) Get(ctx context.Context, id string) (*model.Artifact, error) {
	return s.repo.GetArtifact(ctx, strings.TrimSpace(id))
}

func (s *ArtifactService) List(ctx context.Context, workspaceID, kind string, limit int) ([]model.Artifact, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		workspaceID = "default"
	}
	return s.repo.ListArtifacts(ctx, workspaceID, strings.TrimSpace(kind), limit)
}

func (s *ArtifactService) Open(ctx context.Context, id string) (*model.Artifact, io.ReadCloser, error) {
	artifact, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	reader, err := s.blobs.Open(ctx, artifact.BlobURI)
	if err != nil {
		return nil, nil, err
	}
	return artifact, reader, nil
}

func jsonPath(root any, path string) (any, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "$" {
		return root, nil
	}
	current := root
	for _, segment := range strings.Split(strings.TrimPrefix(path, "$."), ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("路径 %s 的 %s 不是 object", path, segment)
		}
		current, ok = object[segment]
		if !ok {
			return nil, fmt.Errorf("路径 %s 不存在", path)
		}
	}
	return current, nil
}
