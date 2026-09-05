package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cmfy/internal/comfy"
	"cmfy/internal/config"
	"cmfy/internal/jobs"
	"cmfy/internal/output"
	"cmfy/internal/workflow"
)

type Options struct {
	Config       *config.Config
	Client       *comfy.Client
	Jobs         *jobs.Store
	OutputLimits output.Limits
}

type Service struct {
	config       *config.Config
	client       *comfy.Client
	jobs         *jobs.Store
	outputLimits output.Limits
}

type Request struct {
	RequestID  string            `json:"request_id"`
	Workflow   string            `json:"workflow"`
	Prompt     string            `json:"prompt,omitempty"`
	Variables  map[string]string `json:"variables,omitempty"`
	Sets       []string          `json:"sets,omitempty"`
	Parameters map[string]any    `json:"parameters,omitempty"`
	Images     []string          `json:"images,omitempty"`
	Masks      []string          `json:"masks,omitempty"`
	Inputs     []string          `json:"inputs,omitempty"`
	OutputDir  string            `json:"output_dir,omitempty"`
}

type Cancellation struct {
	Schema         string `json:"schema"`
	PromptID       string `json:"prompt_id"`
	PreviousStatus string `json:"previous_status"`
	Status         string `json:"status"`
	Outcome        string `json:"outcome"`
}

type ServerValidation struct {
	Valid              bool     `json:"valid"`
	MissingNodeClasses []string `json:"missing_node_classes"`
}

type Plan struct {
	Schema           string              `json:"schema"`
	Request          Request             `json:"request"`
	WorkflowPath     string              `json:"workflow_path"`
	Alias            string              `json:"alias,omitempty"`
	Contract         workflow.Contract   `json:"contract"`
	Validation       workflow.Validation `json:"validation"`
	ServerValidation *ServerValidation   `json:"server_validation,omitempty"`
	ServerID         string              `json:"server_id"`
	OutputDir        string              `json:"output_dir"`
	template         map[string]any
	defaults         map[string]workflow.VariableMetadata
	values           map[string]string
	inputs           []jobs.Input
}

func New(options Options) *Service {
	return &Service{
		config:       options.Config,
		client:       options.Client,
		jobs:         options.Jobs,
		outputLimits: options.OutputLimits,
	}
}

func (s *Service) Resolve(_ context.Context, request Request) (Plan, error) {
	if s.config == nil {
		return Plan{}, errors.New("engine config is required")
	}
	if strings.TrimSpace(request.Workflow) == "" {
		return Plan{}, errors.New("workflow is required")
	}
	if request.RequestID == "" {
		id, err := requestID()
		if err != nil {
			return Plan{}, err
		}
		request.RequestID = id
	}
	workflowName := request.Workflow
	alias := ""
	if resolved := strings.TrimSpace(s.config.StandardWorkflows[workflowName]); resolved != "" {
		alias = workflowName
		workflowName = resolved
	}
	prompt, workflowPath, defaults, err := workflow.LoadWithVars(s.config.WorkflowsDir, workflowName)
	if err != nil {
		return Plan{}, err
	}
	delete(prompt, "#variables")
	delete(prompt, "variables")
	delete(prompt, "prompt_guidelines")
	delete(prompt, "guidelines")
	if err := workflow.ApplySets(prompt, request.Sets); err != nil {
		return Plan{}, err
	}
	mappings := map[string]string(nil)
	if alias != "" {
		mappings = s.config.StandardWorkflowParams[alias]
	}
	if err := workflow.ApplyParameters(prompt, mappings, request.Parameters); err != nil {
		return Plan{}, err
	}
	contract, err := workflow.Describe(prompt, defaults)
	if err != nil {
		return Plan{}, err
	}
	values := make(map[string]string, len(s.config.Vars)+len(request.Variables)+4)
	for key, value := range s.config.Vars {
		values[key] = value
	}
	name := strings.TrimSuffix(filepath.Base(workflowPath), filepath.Ext(workflowPath))
	for key, value := range s.config.WorkflowVars[name] {
		values[key] = value
	}
	for key, value := range request.Variables {
		values[key] = value
	}
	if request.Prompt != "" {
		values["PROMPT"] = request.Prompt
	}
	inputs, err := describeInputs(request, values, s.config.MaxUploadBytes)
	if err != nil {
		return Plan{}, err
	}
	validationValues := make(map[string]string, len(values)+len(defaults))
	for key, value := range values {
		validationValues[key] = value
	}
	for key, details := range defaults {
		if _, exists := validationValues[key]; !exists && details.Default != "" {
			validationValues[key] = details.Default
		}
	}
	validation, err := workflow.Validate(prompt, validationValues)
	if err != nil {
		return Plan{}, err
	}
	if !validation.Valid {
		return Plan{}, validationError(validation)
	}
	outputDir := request.OutputDir
	if outputDir == "" {
		outputDir = s.config.OutputDir
	}
	serverID := serverIdentity(s.config.ServerURL)
	return Plan{
		Schema:       "cmfy/execution-plan-v1",
		Request:      request,
		WorkflowPath: workflowPath,
		Alias:        alias,
		Contract:     contract,
		Validation:   validation,
		ServerID:     serverID,
		OutputDir:    outputDir,
		template:     prompt,
		defaults:     defaults,
		values:       values,
		inputs:       inputs,
	}, nil
}

func (s *Service) ValidateServer(ctx context.Context, plan Plan) (ServerValidation, error) {
	if s.client == nil {
		return ServerValidation{}, errors.New("server validation requires a client")
	}
	objectInfo, err := s.client.ObjectInfoContext(ctx)
	if err != nil {
		return ServerValidation{}, err
	}
	missing := make([]string, 0)
	for _, className := range plan.Contract.NodeClasses {
		if _, ok := objectInfo[className]; !ok {
			missing = append(missing, className)
		}
	}
	return ServerValidation{Valid: len(missing) == 0, MissingNodeClasses: missing}, nil
}

func (s *Service) Submit(ctx context.Context, plan Plan) (jobs.Record, bool, error) {
	if s.client == nil || s.jobs == nil {
		return jobs.Record{}, false, errors.New("engine submit requires client and job store")
	}
	record, created, err := s.jobs.Reserve(ctx, jobs.Submission{
		RequestID:      plan.Request.RequestID,
		ServerID:       plan.ServerID,
		Workflow:       plan.Request.Workflow,
		WorkflowDigest: plan.Contract.Digest,
		Prompt:         plan.Request.Prompt,
		Parameters:     mergedParameters(plan.Request),
		Inputs:         plan.inputs,
	})
	if err != nil {
		return record, created, err
	}
	if !created {
		if record.Status == "submitting" {
			record, err = s.awaitReservation(ctx, record.RequestID)
		}
		return record, false, err
	}
	graph, err := cloneGraph(plan.template)
	if err != nil {
		_ = s.jobs.Update(ctx, record.RequestID, jobs.Update{Status: "failed", Error: err.Error()})
		return jobs.Record{}, true, err
	}
	values := make(map[string]string, len(plan.values)+len(plan.inputs))
	for key, value := range plan.values {
		values[key] = value
	}
	if err := s.uploadInputs(ctx, plan, values); err != nil {
		_ = s.jobs.Update(ctx, record.RequestID, jobs.Update{Status: "failed", Error: err.Error()})
		return jobs.Record{}, true, err
	}
	workflow.ApplyVarsWithDefaults(graph, values, plan.defaults)
	resolvedValidation, err := workflow.Validate(graph, map[string]string{})
	if err != nil {
		_ = s.jobs.Update(ctx, record.RequestID, jobs.Update{Status: "failed", Error: err.Error()})
		return jobs.Record{}, true, err
	}
	if !resolvedValidation.Valid {
		err := validationError(resolvedValidation)
		_ = s.jobs.Update(ctx, record.RequestID, jobs.Update{Status: "failed", Error: err.Error()})
		return jobs.Record{}, true, err
	}
	promptID, err := s.client.PromptContext(ctx, plan.Request.RequestID, graph)
	if err != nil {
		_ = s.jobs.Update(ctx, record.RequestID, jobs.Update{Status: "failed", Error: err.Error()})
		return jobs.Record{}, true, err
	}
	if err := s.jobs.MarkSubmitted(ctx, record.RequestID, promptID); err != nil {
		return jobs.Record{}, true, fmt.Errorf("record submitted job %s: %w", promptID, err)
	}
	record, err = s.jobs.Get(ctx, promptID)
	return record, true, err
}

func (s *Service) awaitReservation(ctx context.Context, requestID string) (jobs.Record, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		record, err := s.jobs.Get(ctx, requestID)
		if err != nil {
			return jobs.Record{}, err
		}
		if record.Status != "submitting" {
			return record, nil
		}
		select {
		case <-ctx.Done():
			return jobs.Record{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) Observe(ctx context.Context, id string) (jobs.Record, error) {
	if s.client == nil || s.jobs == nil {
		return jobs.Record{}, errors.New("engine observe requires client and job store")
	}
	record, err := s.jobs.Get(ctx, id)
	if err != nil {
		return jobs.Record{}, err
	}
	if record.PromptID == "" {
		return record, nil
	}
	status, statusErr := s.remoteStatus(ctx, record.PromptID)
	if statusErr != nil {
		return jobs.Record{}, statusErr
	}
	if status == "not_found" {
		if record.Status == "cancelling" {
			status = "cancelled"
		} else if time.Since(record.SubmittedAt) < 30*time.Second {
			status = record.Status
		}
	}
	if status != record.Status {
		if err := s.jobs.Update(ctx, record.PromptID, jobs.Update{Status: status}); err != nil {
			return jobs.Record{}, err
		}
	}
	return s.jobs.Get(ctx, record.PromptID)
}

func (s *Service) Cancel(ctx context.Context, id string) (Cancellation, error) {
	if s.client == nil || s.jobs == nil {
		return Cancellation{}, errors.New("engine cancel requires client and job store")
	}
	record, err := s.jobs.Get(ctx, id)
	if err != nil {
		return Cancellation{}, err
	}
	if record.PromptID == "" {
		return Cancellation{}, errors.New("job has no ComfyUI prompt ID")
	}
	status, err := s.remoteStatus(ctx, record.PromptID)
	if err != nil {
		return Cancellation{}, err
	}
	result := Cancellation{Schema: "cmfy/cancellation-v1", PromptID: record.PromptID, PreviousStatus: status, Status: status}
	switch status {
	case "completed", "success", "failed", "error", "cancelled":
		result.Outcome = "already_terminal"
		return result, nil
	case "running":
		if err := s.client.InterruptContext(ctx); err != nil {
			return Cancellation{}, err
		}
	case "queued":
		if err := s.client.DeleteFromQueueContext(ctx, []string{record.PromptID}); err != nil {
			return Cancellation{}, err
		}
	case "not_found":
		result.Outcome = "not_found"
		return result, nil
	default:
		return Cancellation{}, fmt.Errorf("cannot cancel job in status %q", status)
	}
	if err := s.jobs.Update(ctx, record.PromptID, jobs.Update{Status: "cancelling"}); err != nil {
		return Cancellation{}, err
	}
	result.Status = "cancelling"
	result.Outcome = "request_sent"
	return result, nil
}

func (s *Service) Collect(ctx context.Context, id string) (jobs.Record, error) {
	if s.client == nil || s.jobs == nil {
		return jobs.Record{}, errors.New("engine collect requires client and job store")
	}
	record, err := s.jobs.Get(ctx, id)
	if err != nil {
		return jobs.Record{}, err
	}
	if record.PromptID == "" {
		return jobs.Record{}, errors.New("job has no ComfyUI prompt ID")
	}
	history, err := s.client.HistoryContext(ctx, record.PromptID)
	if err != nil {
		return jobs.Record{}, err
	}
	entry, _ := history[record.PromptID].(map[string]any)
	if entry == nil {
		return jobs.Record{}, errors.New("ComfyUI history does not contain the prompt")
	}
	descriptors, err := output.Descriptors(mapValue(entry, "outputs"))
	if err != nil {
		return jobs.Record{}, err
	}
	assets, err := output.Collect(ctx, s.client, recordOutputDir(record, s.config.OutputDir), descriptors, s.outputLimits)
	if err != nil {
		return jobs.Record{}, err
	}
	jobOutputs := make([]jobs.Output, 0, len(assets))
	for _, asset := range assets {
		jobOutputs = append(jobOutputs, jobs.Output{
			Filename:  asset.Filename,
			Subfolder: asset.Subfolder,
			Type:      asset.Type,
			MediaType: asset.MediaType,
			Path:      asset.Path,
			SHA256:    asset.SHA256,
			Size:      asset.Size,
		})
	}
	if err := s.jobs.Update(ctx, record.PromptID, jobs.Update{Status: "completed", Outputs: jobOutputs}); err != nil {
		return jobs.Record{}, err
	}
	return s.jobs.Get(ctx, record.PromptID)
}

func (s *Service) Watch(ctx context.Context, id string, interval time.Duration) (<-chan comfy.Event, <-chan error) {
	events := make(chan comfy.Event)
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)
		record, err := s.jobs.Get(ctx, id)
		if err != nil {
			failures <- err
			return
		}
		if record.PromptID == "" {
			failures <- errors.New("job has no ComfyUI prompt ID")
			return
		}
		if interval <= 0 {
			interval = 1500 * time.Millisecond
		}
		lastStatus := record.Status
		for {
			remoteEvents, remoteFailures := s.client.WatchContext(ctx, record.RequestID, record.PromptID)
			for event := range remoteEvents {
				status := eventStatus(event.Type)
				if status != "" && (status != lastStatus || event.Message != "") {
					if err := s.jobs.Update(ctx, record.PromptID, jobs.Update{Status: status, Error: event.Message, UpdatedAt: event.Time}); err != nil {
						failures <- err
						return
					}
					lastStatus = status
				}
				select {
				case events <- event:
				case <-ctx.Done():
					failures <- ctx.Err()
					return
				}
				if terminalStatus(event.Type) {
					failures <- nil
					return
				}
			}
			_ = <-remoteFailures
			if ctx.Err() != nil {
				failures <- ctx.Err()
				return
			}
			record, err = s.Observe(ctx, id)
			if err != nil {
				failures <- err
				return
			}
			if record.Status != lastStatus {
				event := comfy.Event{Schema: "cmfy/job-event-v1", Type: record.Status, PromptID: record.PromptID, Time: record.UpdatedAt}
				select {
				case events <- event:
				case <-ctx.Done():
					failures <- ctx.Err()
					return
				}
				lastStatus = record.Status
			}
			if terminalStatus(record.Status) {
				failures <- nil
				return
			}
			select {
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			case <-time.After(interval):
			}
		}
	}()
	return events, failures
}

func (s *Service) Wait(ctx context.Context, id string, interval time.Duration) (jobs.Record, error) {
	if interval <= 0 {
		interval = 1500 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		record, err := s.Observe(ctx, id)
		if err != nil {
			return jobs.Record{}, err
		}
		switch record.Status {
		case "completed", "success", "cancelled", "failed", "error":
			return record, nil
		}
		select {
		case <-ctx.Done():
			return jobs.Record{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) remoteStatus(ctx context.Context, promptID string) (string, error) {
	history, historyErr := s.client.HistoryContext(ctx, promptID)
	if historyErr == nil {
		if entry, _ := history[promptID].(map[string]any); entry != nil {
			if status := historyStatus(entry); status != "" {
				return status, nil
			}
		}
	}
	queue, queueErr := s.client.QueueContext(ctx)
	if queueErr == nil {
		if queueContains(queue["queue_running"], promptID) {
			return "running", nil
		}
		if queueContains(queue["queue_pending"], promptID) {
			return "queued", nil
		}
	}
	if historyErr != nil && queueErr != nil {
		return "", errors.Join(historyErr, queueErr)
	}
	return "not_found", nil
}

func (s *Service) uploadInputs(ctx context.Context, plan Plan, values map[string]string) error {
	descriptors := make(map[string]jobs.Input, len(plan.inputs))
	for _, input := range plan.inputs {
		descriptors[input.Path] = input
	}
	groups := []struct {
		prefix string
		paths  []string
	}{{"IMAGE", plan.Request.Images}, {"MASK", plan.Request.Masks}, {"INPUT", plan.Request.Inputs}}
	for _, group := range groups {
		for index, localPath := range group.paths {
			descriptor := descriptors[localPath]
			name := ""
			cached, found, err := s.jobs.GetUpload(ctx, plan.ServerID, descriptor.SHA256)
			if err != nil {
				return err
			}
			if found && s.client.ProbeAssetContext(ctx, cached.RemoteName, "input") == nil {
				name = cached.RemoteName
			}
			if name == "" {
				name, err = s.client.UploadContext(ctx, localPath)
				if err != nil {
					return fmt.Errorf("upload %s: %w", localPath, err)
				}
				if err := s.jobs.PutUpload(ctx, jobs.Upload{ServerID: plan.ServerID, SHA256: descriptor.SHA256, RemoteName: name, Size: descriptor.Size}); err != nil {
					return err
				}
			}
			values[fmt.Sprintf("%s%d", group.prefix, index+1)] = name
			if index == 0 {
				values[group.prefix] = name
			}
		}
	}
	return nil
}

func describeInputs(request Request, values map[string]string, maxBytes int64) ([]jobs.Input, error) {
	if maxBytes <= 0 {
		maxBytes = 1 << 30
	}
	result := make([]jobs.Input, 0, len(request.Images)+len(request.Masks)+len(request.Inputs))
	groups := []struct {
		kind   string
		prefix string
		paths  []string
	}{{"image", "IMAGE", request.Images}, {"mask", "MASK", request.Masks}, {"input", "INPUT", request.Inputs}}
	for _, group := range groups {
		for index, localPath := range group.paths {
			stat, err := os.Stat(localPath)
			if err != nil {
				return nil, fmt.Errorf("inspect input %s: %w", localPath, err)
			}
			if !stat.Mode().IsRegular() {
				return nil, fmt.Errorf("input %s is not a regular file", localPath)
			}
			if stat.Size() > maxBytes {
				return nil, fmt.Errorf("input %s exceeds upload byte limit %d", localPath, maxBytes)
			}
			digest, err := hashPath(localPath)
			if err != nil {
				return nil, err
			}
			result = append(result, jobs.Input{Kind: group.kind, Path: localPath, SHA256: digest, Size: stat.Size()})
			values[fmt.Sprintf("%s%d", group.prefix, index+1)] = "provided"
			if index == 0 {
				values[group.prefix] = "provided"
			}
		}
	}
	return result, nil
}

func hashPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open input %s for hashing: %w", path, err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("hash input %s: %w", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func validationError(validation workflow.Validation) error {
	parts := make([]string, 0, len(validation.Unresolved)+len(validation.Errors))
	if len(validation.Unresolved) > 0 {
		parts = append(parts, "unresolved variables: "+strings.Join(validation.Unresolved, ", "))
	}
	for _, issue := range validation.Errors {
		parts = append(parts, issue.Message)
	}
	return errors.New(strings.Join(parts, "; "))
}

func cloneGraph(source map[string]any) (map[string]any, error) {
	body, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("encode workflow graph: %w", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(body, &clone); err != nil {
		return nil, fmt.Errorf("decode workflow graph: %w", err)
	}
	return clone, nil
}

func historyStatus(entry map[string]any) string {
	status := entry["status"]
	switch value := status.(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(value))
	case map[string]any:
		if completed, _ := value["completed"].(bool); completed {
			return "completed"
		}
		if text, _ := value["status_str"].(string); text != "" {
			return strings.ToLower(strings.TrimSpace(text))
		}
	}
	if len(mapValue(entry, "outputs")) > 0 {
		return "completed"
	}
	return ""
}

func queueContains(raw any, promptID string) bool {
	items, _ := raw.([]any)
	for _, item := range items {
		row, ok := item.([]any)
		if !ok {
			if text, ok := item.(string); ok && text == promptID {
				return true
			}
			continue
		}
		for _, column := range row {
			if text, ok := column.(string); ok && text == promptID {
				return true
			}
		}
	}
	return false
}

func mapValue(source map[string]any, key string) map[string]any {
	value, _ := source[key].(map[string]any)
	return value
}

func mergedParameters(request Request) map[string]any {
	result := make(map[string]any, len(request.Parameters)+2)
	for key, value := range request.Parameters {
		result[key] = value
	}
	if len(request.Variables) > 0 {
		result["variables"] = request.Variables
	}
	if len(request.Sets) > 0 {
		result["sets"] = request.Sets
	}
	if request.OutputDir != "" {
		result["output_dir"] = request.OutputDir
	}
	return result
}

func recordOutputDir(record jobs.Record, fallback string) string {
	if value, ok := record.Parameters["output_dir"].(string); ok && value != "" {
		return value
	}
	return fallback
}

func serverIdentity(serverURL string) string {
	digest := sha256.Sum256([]byte(strings.TrimRight(strings.TrimSpace(serverURL), "/")))
	return "server-" + hex.EncodeToString(digest[:8])
}

func requestID() (string, error) {
	var body [16]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}
	return "request-" + hex.EncodeToString(body[:]), nil
}

func eventStatus(eventType string) string {
	switch eventType {
	case "running", "executing", "progress", "cached", "node_completed", "preview":
		return "running"
	case "completed", "failed", "cancelled":
		return eventType
	default:
		return ""
	}
}

func terminalStatus(status string) bool {
	switch status {
	case "completed", "success", "failed", "error", "cancelled":
		return true
	default:
		return false
	}
}

func StableVariables(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
