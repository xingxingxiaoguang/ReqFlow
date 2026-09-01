package model

import "time"

type Artifact struct {
	ID                    string
	WorkspaceID           string
	Kind                  string
	Name                  string
	BlobURI               string
	ContentHash           string
	ProducerWorkflowRunID string
	ProducerNodeRunID     string
	ProducerAttempt       int
	Metadata              string
	CreatedAt             time.Time
}

const (
	ArtifactMarkdown      = "markdown"
	ArtifactDOCX          = "docx"
	ArtifactPDF           = "pdf"
	ArtifactGraphManifest = "graph_manifest"
	ArtifactJSON          = "json"
)
