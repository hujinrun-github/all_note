package taskdomain

import (
	"context"
	"errors"
	"strings"
	"time"
)

type RoadmapStatus string

const (
	RoadmapStatusDraft     RoadmapStatus = "draft"
	RoadmapStatusActive    RoadmapStatus = "active"
	RoadmapStatusCompleted RoadmapStatus = "completed"
	RoadmapStatusFailed    RoadmapStatus = "failed"
	RoadmapStatusArchived  RoadmapStatus = "archived"
)

type RoadmapNodeType string

const (
	RoadmapNodeStage     RoadmapNodeType = "stage"
	RoadmapNodeTopic     RoadmapNodeType = "topic"
	RoadmapNodeMilestone RoadmapNodeType = "milestone"
)

type RoadmapEdgeType string

type LearningRoadmap struct {
	WorkspaceID string
	ID          string
	ProjectID   string
	Status      RoadmapStatus
	Title       string
	Description string
	Revision    int64
}

type RoadmapNodeProgress struct {
	Tasks     int `json:"tasks"`
	Total     int `json:"total"`
	Open      int `json:"open"`
	Active    int `json:"active"`
	Blocked   int `json:"blocked"`
	Done      int `json:"done"`
	Skipped   int `json:"skipped"`
	Cancelled int `json:"cancelled"`
}

type RoadmapNodeSnapshot struct {
	Node     RoadmapNode
	Progress RoadmapNodeProgress
}
type RoadmapEdge struct {
	WorkspaceID, ID, ProjectID, RoadmapID, FromNodeID, ToNodeID string
	Type                                                        RoadmapEdgeType
	Revision                                                    int64
}
type RoadmapSnapshot struct {
	Roadmap LearningRoadmap
	Nodes   []RoadmapNodeSnapshot
	Edges   []RoadmapEdge
}

var (
	ErrInvalidRoadmapCommand          = errors.New("invalid roadmap command")
	ErrRoadmapNotFound                = errors.New("roadmap not found")
	ErrRoadmapNodeNotFound            = errors.New("roadmap node not found")
	ErrRoadmapRequiresLearningProject = errors.New("roadmap requires learning project")
	ErrRoadmapAlreadyExists           = errors.New("roadmap already exists")
	ErrRoadmapNodeHasTasks            = errors.New("roadmap node has linked tasks")
	ErrRoadmapRevisionConflict        = errors.New("roadmap revision conflict")
)

type RoadmapReader interface {
	GetRoadmapByProject(context.Context, string) (RoadmapSnapshot, error)
	GetRoadmapByID(context.Context, string) (RoadmapSnapshot, error)
	GetRoadmapNode(context.Context, string) (RoadmapNodeSnapshot, error)
}

type RoadmapNodeWrite struct {
	Node             RoadmapNode
	ExpectedRevision int64
}
type RoadmapWriter interface {
	CreateRoadmap(context.Context, LearningRoadmap) error
	CreateRoadmapNode(context.Context, RoadmapNode) error
	SaveRoadmapNode(context.Context, RoadmapNodeWrite) error
	DeleteRoadmapNode(context.Context, string, int64) error
}
type RoadmapCommandTx interface {
	GetProject(context.Context, string) (ProjectSnapshot, error)
	RoadmapReader
	CountRoadmapNodeTasks(context.Context, string) (int, error)
	RoadmapWriter() RoadmapWriter
}
type RoadmapCommandFencer interface {
	BeginFencedRoadmapWrite(context.Context, string, int64, func(RoadmapCommandTx) error) error
}
type RoadmapService struct{ fencer RoadmapCommandFencer }

func NewRoadmapService(f RoadmapCommandFencer) *RoadmapService { return &RoadmapService{fencer: f} }

type CreateRoadmapRequest struct {
	WorkspaceID, ProjectID, RoadmapID, Title, Description, CommandID, ActorID string
	ExpectedRuntimeEpoch                                                      int64
	At                                                                        time.Time
}
type CreateRoadmapNodeRequest struct {
	WorkspaceID, RoadmapID, NodeID, ParentID, Title, Description, CommandID, ActorID string
	Type                                                                             RoadmapNodeType
	Position                                                                         float64
	ExpectedRuntimeEpoch                                                             int64
	At                                                                               time.Time
}
type UpdateRoadmapNodeRequest struct {
	WorkspaceID, RoadmapID, NodeID, ParentID, Title, Description, CommandID, ActorID string
	Type                                                                             RoadmapNodeType
	Position                                                                         float64
	ExpectedRuntimeEpoch, ExpectedRevision                                           int64
	At                                                                               time.Time
}
type DeleteRoadmapNodeRequest struct {
	WorkspaceID, RoadmapID, NodeID, CommandID, ActorID string
	ExpectedRuntimeEpoch, ExpectedRevision             int64
	At                                                 time.Time
}

func (s *RoadmapService) CreateRoadmap(ctx context.Context, r CreateRoadmapRequest) (RoadmapSnapshot, error) {
	if s == nil || s.fencer == nil || !validRoadmapAudit(r.WorkspaceID, r.ProjectID, r.RoadmapID, r.CommandID, r.ActorID, r.ExpectedRuntimeEpoch, r.At) || strings.TrimSpace(r.Title) == "" {
		return RoadmapSnapshot{}, ErrInvalidRoadmapCommand
	}
	var out RoadmapSnapshot
	err := s.fencer.BeginFencedRoadmapWrite(ctx, r.WorkspaceID, r.ExpectedRuntimeEpoch, func(tx RoadmapCommandTx) error {
		p, err := tx.GetProject(ctx, r.ProjectID)
		if err != nil {
			return err
		}
		if p.Project.WorkspaceID != r.WorkspaceID || p.Project.Kind != ProjectKindLearning {
			return ErrRoadmapRequiresLearningProject
		}
		if _, err = tx.GetRoadmapByProject(ctx, r.ProjectID); err == nil {
			return ErrRoadmapAlreadyExists
		}
		if !errors.Is(err, ErrRoadmapNotFound) {
			return err
		}
		rm := LearningRoadmap{WorkspaceID: r.WorkspaceID, ID: r.RoadmapID, ProjectID: r.ProjectID, Status: RoadmapStatusActive, Title: strings.TrimSpace(r.Title), Description: strings.TrimSpace(r.Description), Revision: 1}
		if err = tx.RoadmapWriter().CreateRoadmap(ctx, rm); err != nil {
			return err
		}
		out = RoadmapSnapshot{Roadmap: rm}
		return nil
	})
	return out, err
}

func (s *RoadmapService) CreateNode(ctx context.Context, r CreateRoadmapNodeRequest) (RoadmapNodeSnapshot, error) {
	if s == nil || s.fencer == nil || !validRoadmapAudit(r.WorkspaceID, r.RoadmapID, r.NodeID, r.CommandID, r.ActorID, r.ExpectedRuntimeEpoch, r.At) || strings.TrimSpace(r.Title) == "" || !validRoadmapNodeType(r.Type) {
		return RoadmapNodeSnapshot{}, ErrInvalidRoadmapCommand
	}
	var out RoadmapNodeSnapshot
	err := s.fencer.BeginFencedRoadmapWrite(ctx, r.WorkspaceID, r.ExpectedRuntimeEpoch, func(tx RoadmapCommandTx) error {
		rm, err := tx.GetRoadmapByID(ctx, r.RoadmapID)
		if err != nil {
			return err
		}
		if rm.Roadmap.WorkspaceID != r.WorkspaceID {
			return ErrInvalidRoadmapCommand
		}
		project, err := tx.GetProject(ctx, rm.Roadmap.ProjectID)
		if err != nil {
			return err
		}
		if project.Project.WorkspaceID != r.WorkspaceID || project.Project.Kind != ProjectKindLearning {
			return ErrRoadmapRequiresLearningProject
		}
		if r.ParentID != "" {
			parent, err := tx.GetRoadmapNode(ctx, r.ParentID)
			if err != nil {
				return err
			}
			if parent.Node.RoadmapID != r.RoadmapID || parent.Node.ProjectID != rm.Roadmap.ProjectID {
				return ErrInvalidRoadmapCommand
			}
		}
		n := RoadmapNode{WorkspaceID: r.WorkspaceID, ID: r.NodeID, ProjectID: rm.Roadmap.ProjectID, RoadmapID: r.RoadmapID, ParentID: r.ParentID, Title: strings.TrimSpace(r.Title), Description: strings.TrimSpace(r.Description), Type: r.Type, Position: r.Position, Revision: 1}
		if err = tx.RoadmapWriter().CreateRoadmapNode(ctx, n); err != nil {
			return err
		}
		out = RoadmapNodeSnapshot{Node: n}
		return nil
	})
	return out, err
}

func (s *RoadmapService) UpdateNode(ctx context.Context, r UpdateRoadmapNodeRequest) (RoadmapNodeSnapshot, error) {
	if s == nil || s.fencer == nil || !validRoadmapAudit(r.WorkspaceID, r.RoadmapID, r.NodeID, r.CommandID, r.ActorID, r.ExpectedRuntimeEpoch, r.At) || r.ExpectedRevision < 1 || strings.TrimSpace(r.Title) == "" || !validRoadmapNodeType(r.Type) {
		return RoadmapNodeSnapshot{}, ErrInvalidRoadmapCommand
	}
	var out RoadmapNodeSnapshot
	err := s.fencer.BeginFencedRoadmapWrite(ctx, r.WorkspaceID, r.ExpectedRuntimeEpoch, func(tx RoadmapCommandTx) error {
		cur, err := tx.GetRoadmapNode(ctx, r.NodeID)
		if err != nil {
			return err
		}
		if cur.Node.WorkspaceID != r.WorkspaceID || cur.Node.RoadmapID != r.RoadmapID {
			return ErrInvalidRoadmapCommand
		}
		if cur.Node.Revision != r.ExpectedRevision {
			return ErrRoadmapRevisionConflict
		}
		if r.ParentID == r.NodeID {
			return ErrInvalidRoadmapCommand
		}
		if r.ParentID != "" {
			p, err := tx.GetRoadmapNode(ctx, r.ParentID)
			if err != nil {
				return err
			}
			if p.Node.RoadmapID != r.RoadmapID {
				return ErrInvalidRoadmapCommand
			}
		}
		n := cur.Node
		n.ParentID = r.ParentID
		n.Title = strings.TrimSpace(r.Title)
		n.Description = strings.TrimSpace(r.Description)
		n.Type = r.Type
		n.Position = r.Position
		n.Revision++
		if err = tx.RoadmapWriter().SaveRoadmapNode(ctx, RoadmapNodeWrite{Node: n, ExpectedRevision: r.ExpectedRevision}); err != nil {
			return err
		}
		out = RoadmapNodeSnapshot{Node: n, Progress: cur.Progress}
		return nil
	})
	return out, err
}

func (s *RoadmapService) DeleteNode(ctx context.Context, r DeleteRoadmapNodeRequest) error {
	if s == nil || s.fencer == nil || !validRoadmapAudit(r.WorkspaceID, r.RoadmapID, r.NodeID, r.CommandID, r.ActorID, r.ExpectedRuntimeEpoch, r.At) || r.ExpectedRevision < 1 {
		return ErrInvalidRoadmapCommand
	}
	return s.fencer.BeginFencedRoadmapWrite(ctx, r.WorkspaceID, r.ExpectedRuntimeEpoch, func(tx RoadmapCommandTx) error {
		cur, err := tx.GetRoadmapNode(ctx, r.NodeID)
		if err != nil {
			return err
		}
		if cur.Node.WorkspaceID != r.WorkspaceID || cur.Node.RoadmapID != r.RoadmapID {
			return ErrInvalidRoadmapCommand
		}
		if cur.Node.Revision != r.ExpectedRevision {
			return ErrRoadmapRevisionConflict
		}
		n, err := tx.CountRoadmapNodeTasks(ctx, r.NodeID)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrRoadmapNodeHasTasks
		}
		return tx.RoadmapWriter().DeleteRoadmapNode(ctx, r.NodeID, r.ExpectedRevision)
	})
}

func validRoadmapAudit(values ...any) bool {
	if len(values) != 7 {
		return false
	}
	for _, i := range []int{0, 1, 2, 3, 4} {
		if strings.TrimSpace(values[i].(string)) == "" {
			return false
		}
	}
	return values[5].(int64) > 0 && !values[6].(time.Time).IsZero()
}
func validRoadmapNodeType(t RoadmapNodeType) bool {
	return t == RoadmapNodeStage || t == RoadmapNodeTopic || t == RoadmapNodeMilestone
}
