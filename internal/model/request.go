package model

type RunnerRequest struct {
	DeliveryID     string
	Owner          string
	Repository     string
	FullName       string
	RepositoryID   int64
	InstallationID int64
	JobID          int64
	JobName        string
	RunID          int64
	WorkflowName   string
	Labels         []string
	Sender         string
}
