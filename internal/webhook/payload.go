package webhook

import (
	"fmt"
	"regexp"
	"strings"

	"runnerd/internal/model"
)

type Payload struct {
	Action      string `json:"action"`
	WorkflowJob struct {
		ID           int64    `json:"id"`
		Name         string   `json:"name"`
		Status       string   `json:"status"`
		Labels       []string `json:"labels"`
		RunID        int64    `json:"run_id"`
		WorkflowName string   `json:"workflow_name"`
	} `json:"workflow_job"`
	Repository struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

var segment = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,100}$`)

func (p Payload) Request(delivery string) (model.RunnerRequest, error) {
	r := p.Repository
	if !segment.MatchString(r.Owner.Login) || r.Owner.Login == "." || r.Owner.Login == ".." || !segment.MatchString(r.Name) || r.Name == "." || r.Name == ".." || !strings.EqualFold(r.FullName, r.Owner.Login+"/"+r.Name) {
		return model.RunnerRequest{}, fmt.Errorf("invalid repository identity")
	}
	if p.Installation.ID <= 0 || p.WorkflowJob.ID <= 0 || p.WorkflowJob.RunID <= 0 {
		return model.RunnerRequest{}, fmt.Errorf("installation and workflow job/run IDs must be positive")
	}
	return model.RunnerRequest{DeliveryID: delivery, Owner: r.Owner.Login, Repository: r.Name, FullName: r.FullName, RepositoryID: r.ID, InstallationID: p.Installation.ID, JobID: p.WorkflowJob.ID, JobName: p.WorkflowJob.Name, RunID: p.WorkflowJob.RunID, WorkflowName: p.WorkflowJob.WorkflowName, Labels: append([]string(nil), p.WorkflowJob.Labels...), Sender: p.Sender.Login}, nil
}
