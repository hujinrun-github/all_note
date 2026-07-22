package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
	"golang.org/x/net/html"
)

type roadmapDraft struct {
	Title string              `json:"title"`
	Goal  string              `json:"goal"`
	Nodes []model.RoadmapNode `json:"nodes"`
	Edges []model.RoadmapEdge `json:"edges"`
}

const (
	defaultAIProvider          = "deepseek"
	defaultAIBaseURL           = "https://api.deepseek.com"
	defaultAIModel             = "deepseek-v4-pro"
	defaultAIRequestTimeout    = 120 * time.Second
	defaultArticleProvider     = "duckduckgo"
	highSignalArticleHint      = `(popular OR "most read" OR upvoted OR recommended) high quality guide article tutorial`
	roadmapNodeMinGapX         = 250
	roadmapNodeMinGapY         = 95
	roadmapLayoutStepY         = 130
	roadmapBranchHorizontalGap = 340
	roadmapBranchVerticalGap   = 145
	minArticleSearchResults    = 10
	maxArticleSearchResults    = 20
)

type articleSearchSource struct {
	ID        string
	Label     string
	QueryHint string
	MockURL   string
}

var articleSearchSourceCatalog = []articleSearchSource{
	{ID: "google", Label: "Google/通用", QueryHint: "Google technical blog community discussion", MockURL: "https://www.google.com/search"},
	{ID: "medium", Label: "Medium", QueryHint: "site:medium.com", MockURL: "https://medium.com/search"},
	{ID: "reddit", Label: "Reddit", QueryHint: "site:reddit.com", MockURL: "https://www.reddit.com/search/"},
	{ID: "devto", Label: "Dev.to", QueryHint: "site:dev.to", MockURL: "https://dev.to/search"},
	{ID: "hashnode", Label: "Hashnode", QueryHint: "site:hashnode.com", MockURL: "https://hashnode.com/search"},
	{ID: "stackoverflow", Label: "Stack Overflow", QueryHint: "site:stackoverflow.com", MockURL: "https://stackoverflow.com/search"},
	{ID: "github", Label: "GitHub", QueryHint: "site:github.com", MockURL: "https://github.com/search"},
	{ID: "official", Label: "官方文档", QueryHint: "official documentation docs guide", MockURL: "https://example.com/docs"},
	{ID: "technical", Label: "技术博客", QueryHint: "site:freecodecamp.org OR site:web.dev OR site:martinfowler.com OR site:smashingmagazine.com", MockURL: "https://web.dev/s/results"},
}

var (
	articleHTTPClient      = &http.Client{Timeout: 20 * time.Second}
	bingSearchURL          = "https://www.bing.com/search"
	devToArticlesURL       = "https://dev.to/api/articles"
	stackExchangeSearchURL = "https://api.stackexchange.com/2.3/search/advanced"
)

func GenerateLearningRoadmap(ctx context.Context, store storage.Store, projectID string) (*model.LearningRoadmap, error) {
	return GenerateLearningRoadmapWithPrompt(ctx, store, projectID, "")
}

func GenerateLearningRoadmapWithPrompt(ctx context.Context, store storage.Store, projectID string, customPrompt string) (*model.LearningRoadmap, error) {
	return GenerateLearningRoadmapWithPromptAndAI(ctx, store, projectID, customPrompt, nil)
}

type TextGenerator interface {
	Generate(context.Context, string, string) (string, error)
}

func GenerateLearningRoadmapWithPromptAndAI(ctx context.Context, store storage.Store, projectID string, customPrompt string, generator TextGenerator) (*model.LearningRoadmap, error) {
	return generateLearningRoadmapWithPolicy(ctx, store, projectID, customPrompt, generator, true, true)
}

func GenerateLearningRoadmapWithPromptAndAIPolicy(ctx context.Context, store storage.Store, projectID string, customPrompt string, generator TextGenerator, allowTemplateFallback bool) (*model.LearningRoadmap, error) {
	return generateLearningRoadmapWithPolicy(ctx, store, projectID, customPrompt, generator, allowTemplateFallback, false)
}

func generateLearningRoadmapWithPolicy(ctx context.Context, store storage.Store, projectID string, customPrompt string, generator TextGenerator, allowTemplateFallback, useLegacyEnvironment bool) (*model.LearningRoadmap, error) {
	customPrompt = strings.TrimSpace(customPrompt)
	project, err := store.Tasks().GetProjectByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.Type != "learning" {
		return nil, fmt.Errorf("project is not a learning project")
	}

	var draft *roadmapDraft
	if generator != nil {
		draft, err = generateRoadmapWithGenerator(ctx, *project, customPrompt, generator)
	} else if !useLegacyEnvironment && customPrompt != "" {
		err = errors.New("an available AI service is required for an edited roadmap prompt")
	} else if !useLegacyEnvironment && allowTemplateFallback {
		draft = mockRoadmapDraft(*project)
	} else if !useLegacyEnvironment {
		err = errors.New("roadmap AI capability is disabled")
	} else {
		draft, err = generateRoadmapDraft(*project, customPrompt)
	}
	if err != nil {
		if customPrompt != "" {
			return nil, fmt.Errorf("custom roadmap prompt could not be applied: %w", err)
		}
		if (!useLegacyEnvironment && !allowTemplateFallback) || (useLegacyEnvironment && !shouldUseFallbackRoadmapDraft(err)) {
			_, _ = store.Roadmaps().SaveFailedLearningRoadmap(ctx, project.ID, project.Name+" 学习路线", project.Description)
			return nil, err
		}
		draft = mockRoadmapDraft(*project)
	}

	sanitizeGeneratedRoadmapDraft(draft)
	ensureGeneratedRoadmapDepthAndDetail(draft, *project)
	normalizeGeneratedRoadmapToLinearPath(draft)

	roadmap := &model.LearningRoadmap{
		ProjectID: project.ID,
		Title:     draft.Title,
		Goal:      draft.Goal,
		Status:    "ready",
		Nodes:     draft.Nodes,
		Edges:     draft.Edges,
	}
	saved, err := store.Roadmaps().ReplaceLearningRoadmap(ctx, roadmap)
	if err != nil {
		return nil, err
	}
	normalizeRoadmapDisplayLayout(saved)
	return saved, nil
}

func generateRoadmapWithGenerator(ctx context.Context, project model.TaskProject, customPrompt string, generator TextGenerator) (*roadmapDraft, error) {
	content, err := generator.Generate(ctx, buildRoadmapSystemPrompt(), buildRoadmapUserPrompt(project, customPrompt))
	if err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		if newline := strings.IndexByte(content, '\n'); newline >= 0 {
			content = content[newline+1:]
		}
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(content), "```"))
	}
	var draft roadmapDraft
	if err := json.Unmarshal([]byte(content), &draft); err != nil {
		return nil, err
	}
	normalizeRoadmapDraftIDs(&draft)
	if strings.TrimSpace(draft.Title) == "" {
		draft.Title = project.Name + " 学习路线"
	}
	if strings.TrimSpace(draft.Goal) == "" {
		draft.Goal = "通过项目实战掌握 " + project.Name
	}
	if len(draft.Nodes) == 0 {
		return nil, errors.New("AI response did not include roadmap nodes")
	}
	return &draft, nil
}

func GetLearningRoadmap(ctx context.Context, store storage.Store, projectID string) (*model.LearningRoadmap, error) {
	roadmap, err := store.Roadmaps().GetLearningRoadmap(ctx, projectID)
	if err != nil {
		return nil, err
	}
	filterRoadmapResourcesByNodeRelevance(roadmap)
	normalizeRoadmapDisplayLayout(roadmap)
	return roadmap, nil
}

func filterRoadmapResourcesByNodeRelevance(roadmap *model.LearningRoadmap) {
	if roadmap == nil {
		return
	}
	for nodeIndex := range roadmap.Nodes {
		node := &roadmap.Nodes[nodeIndex]
		searched := make([]model.RoadmapResource, 0, len(node.Resources))
		for _, resource := range node.Resources {
			if resource.AddedBy == "search" {
				searched = append(searched, resource)
			}
		}
		relevant := filterAndRankArticleResources(*node, nil, searched, len(searched))
		relevantURLs := make(map[string]bool, len(relevant))
		for _, resource := range relevant {
			relevantURLs[resource.URL] = true
		}
		filtered := make([]model.RoadmapResource, 0, len(node.Resources))
		for _, resource := range node.Resources {
			if resource.AddedBy != "search" || relevantURLs[resource.URL] {
				filtered = append(filtered, resource)
			}
		}
		node.Resources = filtered
	}
}

func UpdateRoadmapNode(ctx context.Context, store storage.Store, id string, req *model.UpdateRoadmapNodeRequest) (*model.RoadmapNode, error) {
	return store.Roadmaps().UpdateRoadmapNode(ctx, id, req)
}

func CreateRoadmapNode(ctx context.Context, store storage.Store, roadmapID string, req *model.CreateRoadmapNodeRequest) (*model.RoadmapNode, error) {
	if req == nil {
		return nil, errors.New("request body is required")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, errors.New("title is required")
	}

	roadmap, err := store.Roadmaps().GetLearningRoadmapByID(ctx, roadmapID)
	if err != nil {
		return nil, err
	}

	pathType := normalizeRoadmapNodePathType(req.PathType)
	nodeType := normalizeRoadmapNodeType(req.Type)
	status := normalizeRoadmapNodeStatus(req.Status)
	orderIndex, x, y := nextRoadmapNodePlacement(roadmap)

	var parentID *string
	var edge *model.RoadmapEdge
	if req.ParentID != nil && strings.TrimSpace(*req.ParentID) != "" {
		trimmedParentID := strings.TrimSpace(*req.ParentID)
		parentNode, ok := roadmapNodeByID(roadmap.Nodes, trimmedParentID)
		if !ok {
			return nil, sql.ErrNoRows
		}
		parentID = &trimmedParentID
		x, y = childRoadmapNodePlacement(parentNode, pathType)
		edge = &model.RoadmapEdge{
			RoadmapID:    roadmap.ID,
			SourceNodeID: trimmedParentID,
			Style:        normalizeRoadmapEdgeStyle(req.EdgeStyle, pathType),
		}
	}

	if req.X != nil {
		x = *req.X
	}
	if req.Y != nil {
		y = *req.Y
	}

	return store.Roadmaps().CreateRoadmapNode(ctx, &model.RoadmapNode{
		RoadmapID:          roadmap.ID,
		ParentID:           parentID,
		Type:               nodeType,
		Title:              title,
		Description:        strings.TrimSpace(req.Description),
		PathType:           pathType,
		Status:             status,
		Deliverable:        strings.TrimSpace(req.Deliverable),
		AcceptanceCriteria: strings.TrimSpace(req.AcceptanceCriteria),
		X:                  x,
		Y:                  y,
		OrderIndex:         orderIndex,
	}, edge)
}

func DeleteRoadmapNode(ctx context.Context, store storage.Store, id string) error {
	return store.Roadmaps().DeleteRoadmapNode(ctx, id)
}

func UpdateRoadmapLayout(ctx context.Context, store storage.Store, roadmapID string, nodes []model.RoadmapLayoutNode) error {
	return store.Roadmaps().UpdateRoadmapLayout(ctx, roadmapID, nodes)
}

func OptimizeRoadmapLayout(ctx context.Context, store storage.Store, roadmapID string) (*model.LearningRoadmap, error) {
	roadmap, err := store.Roadmaps().GetLearningRoadmapByID(ctx, roadmapID)
	if err != nil {
		return nil, err
	}
	draft := &roadmapDraft{
		Title: roadmap.Title,
		Goal:  roadmap.Goal,
		Nodes: roadmap.Nodes,
		Edges: roadmap.Edges,
	}
	optimizeRoadmapDraftLayout(draft)

	layoutNodes := make([]model.RoadmapLayoutNode, 0, len(draft.Nodes))
	for _, node := range draft.Nodes {
		layoutNodes = append(layoutNodes, model.RoadmapLayoutNode{ID: node.ID, X: node.X, Y: node.Y})
	}
	if err := store.Roadmaps().UpdateRoadmapLayout(ctx, roadmap.ID, layoutNodes); err != nil {
		return nil, err
	}
	optimized, err := store.Roadmaps().GetLearningRoadmapByID(ctx, roadmap.ID)
	if err != nil {
		return nil, err
	}
	normalizeRoadmapDisplayLayout(optimized)
	return optimized, nil
}

func SearchRoadmapNodeResources(ctx context.Context, store storage.Store, nodeID string, req *model.SearchRoadmapResourcesRequest) (*model.RoadmapResourceSearchResult, error) {
	node, err := store.Roadmaps().GetRoadmapNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	var sources []string
	var customQuery string
	if req != nil {
		sources = req.Sources
		customQuery = strings.TrimSpace(req.Query)
	}
	linkedTasks, _, err := store.Tasks().List(ctx, storage.TaskFilter{RoadmapNodeID: node.ID, Page: 1, PageSize: 10000})
	if err != nil {
		return nil, err
	}
	searchNode := *node
	if customQuery != "" {
		searchNode.ArticleSearchQueries = []string{customQuery}
	}
	selectedSources := selectedArticleSearchSources(sources)
	query := buildArticleSearchQuery(searchNode, linkedTasks, selectedSources)
	results, err := searchArticleResources(searchNode, linkedTasks, sources, articleSearchMaxResults())
	if err != nil {
		return nil, err
	}
	for index := range results {
		if results[index].ID == "" {
			results[index].ID = randomLocalID("resource-candidate")
		}
		results[index].NodeID = node.ID
		if results[index].SourceType == "" {
			results[index].SourceType = "article"
		}
		if results[index].AddedBy == "" {
			results[index].AddedBy = "search"
		}
	}
	return &model.RoadmapResourceSearchResult{NodeID: node.ID, Query: query, Resources: results}, nil
}

func AddRoadmapNodeResource(ctx context.Context, store storage.Store, nodeID string, req *model.CreateRoadmapResourceRequest) (*model.RoadmapResource, error) {
	if _, err := store.Roadmaps().GetRoadmapNode(ctx, nodeID); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(req.Title)
	url := strings.TrimSpace(req.URL)
	if title == "" || url == "" {
		return nil, errors.New("title and url are required")
	}
	resource := &model.RoadmapResource{
		NodeID:     nodeID,
		Title:      title,
		URL:        url,
		Summary:    strings.TrimSpace(req.Summary),
		SourceType: "manual",
		AddedBy:    "user",
	}
	if err := store.Roadmaps().AddRoadmapResource(ctx, resource); err != nil {
		return nil, err
	}
	return resource, nil
}

func DeleteRoadmapResource(ctx context.Context, store storage.Store, id string) error {
	return store.Roadmaps().DeleteRoadmapResource(ctx, id)
}

func generateRoadmapDraft(project model.TaskProject, customPrompt string) (*roadmapDraft, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("AI_PROVIDER")))
	if provider == "" {
		provider = defaultAIProvider
	}
	if provider == "mock" || provider == "none" {
		return mockRoadmapDraft(project), nil
	}
	if provider == "invalid-json" {
		return nil, errors.New("AI response was not valid JSON")
	}
	return generateOpenAICompatibleRoadmap(project, customPrompt)
}

func shouldUseFallbackRoadmapDraft(err error) bool {
	if err == nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(os.Getenv("AI_PROVIDER"))) != "invalid-json"
}

func legacyMockRoadmapDraft(project model.TaskProject) *roadmapDraft {
	nodeIDs := []string{randomLocalID("node"), randomLocalID("node"), randomLocalID("node"), randomLocalID("node"), randomLocalID("node"), randomLocalID("node")}
	nodes := []model.RoadmapNode{
		{
			ID:                 nodeIDs[0],
			Type:               "phase",
			Title:              "项目目标与环境",
			Description:        "明确最终要做出的项目，准备开发环境和基础工具链。",
			PathType:           "required",
			Status:             "active",
			Deliverable:        project.Name + " 项目说明和开发环境",
			AcceptanceCriteria: "能描述项目目标，并完成本地运行环境准备。",
			X:                  0,
			Y:                  20,
			OrderIndex:         0,
		},
		{
			ID:                 nodeIDs[1],
			Type:               "module",
			Title:              "核心概念速通",
			Description:        "学习完成项目必须理解的核心概念，不追求百科式铺开。",
			PathType:           "required",
			Status:             "todo",
			Deliverable:        "核心概念笔记与最小示例",
			AcceptanceCriteria: "能用自己的话解释关键概念，并跑通一个最小 demo。",
			X:                  0,
			Y:                  130,
			OrderIndex:         1,
		},
		{
			ID:                 nodeIDs[2],
			Type:               "choice",
			Title:              "官方文档路线",
			Description:        "优先阅读官方指南，适合建立准确理解。",
			PathType:           "recommended",
			Status:             "todo",
			Deliverable:        "官方文档阅读清单",
			AcceptanceCriteria: "完成关键章节阅读并记录疑问。",
			X:                  -270,
			Y:                  130,
			OrderIndex:         2,
		},
		{
			ID:                 nodeIDs[3],
			Type:               "choice",
			Title:              "实战教程路线",
			Description:        "选择一篇项目教程先跑通，再反向补概念。",
			PathType:           "alternative",
			Status:             "todo",
			Deliverable:        "可运行的教程项目",
			AcceptanceCriteria: "能独立复现教程关键步骤。",
			X:                  270,
			Y:                  130,
			OrderIndex:         3,
		},
		{
			ID:                 nodeIDs[4],
			Type:               "task",
			Title:              "实现第一个可用版本",
			Description:        "围绕一个最小闭环实现核心功能，不在第一版追求完整。",
			PathType:           "required",
			Status:             "todo",
			Deliverable:        project.Name + " MVP",
			AcceptanceCriteria: "项目能本地运行，核心流程可以手动验证。",
			X:                  0,
			Y:                  260,
			OrderIndex:         4,
		},
		{
			ID:                 nodeIDs[5],
			Type:               "task",
			Title:              "复盘与扩展",
			Description:        "复盘项目问题，选择一个扩展点继续迭代。",
			PathType:           "optional",
			Status:             "todo",
			Deliverable:        "复盘文档和下一轮迭代清单",
			AcceptanceCriteria: "列出至少 3 个改进点，并完成其中 1 个。",
			X:                  0,
			Y:                  390,
			OrderIndex:         5,
		},
	}
	edges := []model.RoadmapEdge{
		{ID: randomLocalID("edge"), SourceNodeID: nodeIDs[0], TargetNodeID: nodeIDs[1], Style: "solid"},
		{ID: randomLocalID("edge"), SourceNodeID: nodeIDs[1], TargetNodeID: nodeIDs[2], Style: "dotted"},
		{ID: randomLocalID("edge"), SourceNodeID: nodeIDs[1], TargetNodeID: nodeIDs[3], Style: "dotted"},
		{ID: randomLocalID("edge"), SourceNodeID: nodeIDs[1], TargetNodeID: nodeIDs[4], Style: "solid"},
		{ID: randomLocalID("edge"), SourceNodeID: nodeIDs[4], TargetNodeID: nodeIDs[5], Style: "solid"},
	}
	return &roadmapDraft{
		Title: project.Name + " 学习路线",
		Goal:  "通过项目实战掌握 " + project.Name,
		Nodes: nodes,
		Edges: edges,
	}
}

func mockRoadmapDraft(project model.TaskProject) *roadmapDraft {
	type roadmapStep struct {
		NodeType           string
		Title              string
		Description        string
		Deliverable        string
		AcceptanceCriteria string
		Queries            []string
	}

	projectGoal := strings.TrimSpace(project.Description)
	if projectGoal == "" {
		projectGoal = "系统掌握 " + project.Name + " 并能独立完成综合实践"
	}
	steps := []roadmapStep{
		{"phase", "明确目标与能力诊断", "把学习目标拆成可验证的能力项，并通过一组基线题或小任务确认当前起点。", "目标清单、能力矩阵和基线测试记录", "目标包含明确完成日期和成果形式，并能指出至少 3 个当前薄弱点。", []string{project.Name + " learning objectives assessment", project.Name + " beginner skill checklist"}},
		{"task", "搭建环境与资料体系", "准备完成整条路线需要的工具、练习环境和统一的笔记归档方式。", "可运行的学习环境和资料索引", "环境能够独立复现，资料按主题分类且包含官方文档入口。", []string{project.Name + " setup guide", project.Name + " official documentation getting started"}},
		{"module", "建立核心概念地图", "先形成知识全景，理解关键概念之间的依赖关系，再进入细节学习。", "一张核心概念关系图和术语表", "能够用自己的话解释至少 10 个核心术语及其相互关系。", []string{project.Name + " core concepts overview", project.Name + " fundamentals roadmap"}},
		{"module", "掌握基础知识单元一", "学习最基础、使用频率最高的知识，并通过小例子验证理解。", "基础知识笔记和 5 个最小示例", "不查资料能够解释关键原理，并独立完成全部最小示例。", []string{project.Name + " fundamentals tutorial", project.Name + " basic examples"}},
		{"module", "掌握基础知识单元二", "补齐与上一单元直接相连的关键知识，形成可连续运用的基础能力。", "第二组知识笔记和对比示例", "能够说明两个基础单元的边界、联系及常见误区。", []string{project.Name + " essential concepts guide", project.Name + " common mistakes beginners"}},
		{"task", "完成基础专项练习", "用由易到难的练习检验基础知识，记录错误类型而不只记录答案。", "不少于 20 题或 3 个专项练习及错题记录", "正确率达到 80%，所有错误都有原因分析和改进动作。", []string{project.Name + " practice exercises", project.Name + " beginner project exercises"}},
		{"module", "构建进阶知识框架", "进入影响真实应用质量的进阶主题，理解它们何时需要以及如何取舍。", "进阶主题清单和决策对照表", "能针对 3 个典型场景选择合适方法并解释理由。", []string{project.Name + " advanced concepts", project.Name + " best practices tradeoffs"}},
		{"module", "拆解高频难点", "集中处理最容易混淆或导致失败的难点，通过反例和调试过程建立判断力。", "难点案例集、反例和排查清单", "能够复现并解决至少 5 个常见问题，说明根因而不是只给结论。", []string{project.Name + " common pitfalls debugging", project.Name + " troubleshooting guide"}},
		{"task", "建立实战工作流", "把零散知识组织成从分析、实施、验证到复盘的稳定工作流程。", "一份可重复使用的实战步骤模板", "使用模板完成一次小任务，过程包含输入、决策、验证和复盘。", []string{project.Name + " practical workflow", project.Name + " project best practices"}},
		{"task", "完成第一轮综合实践", "在允许查阅资料的情况下完成一个覆盖核心知识的综合任务，重点保证过程完整。", project.Name + " 第一轮综合成果", "成果可运行或可演示，覆盖核心能力项，并记录关键设计决策。", []string{project.Name + " hands on project tutorial", project.Name + " end to end example"}},
		{"module", "复盘结果与识别差距", "对照验收标准检查第一轮实践，区分知识缺口、熟练度不足和流程问题。", "问题清单、根因分类和强化计划", "每个问题都有证据、根因和下一步练习，且按影响程度排序。", []string{project.Name + " project review checklist", project.Name + " self assessment rubric"}},
		{"task", "开展针对性强化训练", "围绕差距清单进行短周期刻意练习，每次只解决一个明确弱点。", "不少于 5 组强化练习和前后对比记录", "关键弱项的正确率或完成速度较基线提升至少 20%。", []string{project.Name + " deliberate practice", project.Name + " advanced exercises"}},
		{"task", "完成第二轮独立实践", "减少对教程和答案的依赖，从空白开始独立完成新的综合任务。", project.Name + " 第二轮独立成果", "在限定时间内独立完成，核心步骤无需照抄教程，结果通过自测。", []string{project.Name + " independent project ideas", project.Name + " intermediate project challenge"}},
		{"task", "完成完整模拟或正式交付", "按照真实考试、工作或项目环境完成一次完整演练，覆盖时间管理和质量检查。", "完整模拟记录或可交付项目", "在规定条件下达到目标分数或通过预设的质量检查清单。", []string{project.Name + " mock exam full practice", project.Name + " production project checklist"}},
		{"phase", "执行最终验收", "用最初定义的能力矩阵逐项验收，并通过讲解、演示或测试证明学习结果。", "最终能力报告和成果演示", "所有核心能力项都有可核验的证据，未达标项有明确补救计划。", []string{project.Name + " final assessment", project.Name + " competency checklist"}},
		{"task", "建立复习与持续维护计划", "把已掌握内容转成可持续的复习节奏，安排后续实践和定期复测。", "未来 8 周复习表和下一阶段计划", "计划包含复习间隔、复测方式和至少 2 个后续实践主题。", []string{project.Name + " spaced repetition plan", project.Name + " continuing learning roadmap"}},
	}

	nodes := make([]model.RoadmapNode, 0, len(steps))
	for index, step := range steps {
		status := "todo"
		if index == 0 {
			status = "active"
		}
		nodes = append(nodes, model.RoadmapNode{
			ID:                   randomLocalID("node"),
			Type:                 step.NodeType,
			Title:                step.Title,
			Description:          step.Description,
			PathType:             "required",
			Status:               status,
			Deliverable:          step.Deliverable,
			AcceptanceCriteria:   step.AcceptanceCriteria,
			OrderIndex:           index,
			ArticleSearchQueries: step.Queries,
		})
	}

	draft := &roadmapDraft{
		Title: project.Name + " 完整学习路径",
		Goal:  projectGoal,
		Nodes: nodes,
	}
	normalizeGeneratedRoadmapToLinearPath(draft)
	return draft
}

func buildRoadmapSystemPrompt() string {
	return strings.Join([]string{
		"Return only valid JSON and no markdown.",
		"Build a complete project-practice learning roadmap as graph data for execution inside a task app.",
		"The roadmap must be strictly linear with 14 to 20 nodes and no branches, optional detours, alternatives, or choice nodes.",
		"Create one continuous required path from the starting assessment to final validation and continued maintenance.",
		"Use 4 to 6 phase/module milestones connected by concrete practice tasks, but keep every node in one sequential order.",
		"Cover goal diagnosis, setup, foundations, core knowledge, deliberate practice, integrated practice, gap review, reinforcement, independent practice, final assessment, and retention planning.",
		"Every node must include a detailed Chinese description, a concrete deliverable, and a measurable acceptance_criteria.",
		"Each node must include article_search_queries with 2 or 3 English search queries for online articles, official documentation, and high-quality tutorials.",
		"Do not invent article URLs. Provide search queries only; the backend will query online articles and attach real links.",
		"Use Chinese titles and descriptions.",
		"Node schema: id,type,title,description,path_type,status,deliverable,acceptance_criteria,x,y,order_index,article_search_queries.",
		"Allowed node.type values: phase,module,task. Do not use choice.",
		"Every node.path_type must be required.",
		"Allowed node.status values: active,todo.",
		"Edge schema: id,source_node_id,target_node_id,style.",
		"Create exactly nodes.length - 1 edges. Every edge must connect each node to the next node by order_index and use style=solid.",
		"Return JSON object schema: {\"title\":\"...\",\"goal\":\"...\",\"nodes\":[...],\"edges\":[...]}.",
	}, "\n")
}

func buildRoadmapUserPrompt(project model.TaskProject, customPrompt string) string {
	prompt := fmt.Sprintf(strings.Join([]string{
		"Project name: %s",
		"Description: %s",
		"Generate a comprehensive 14 to 20 node learning path tailored to this exact project.",
		"Do not add branches or choices. Give the learner the complete recommended path directly.",
		"Increase detail gradually from fundamentals to independent execution and final validation.",
		"Every step must produce an observable result and have a measurable completion standard.",
		"Give x/y positions in a readable sequential layout; the backend will normalize them if needed.",
	}, "\n"), project.Name, project.Description)
	if customPrompt = strings.TrimSpace(customPrompt); customPrompt != "" {
		prompt += "\nUser-defined generation requirements:\n" + customPrompt
	}
	return prompt
}

func normalizeGeneratedRoadmapToLinearPath(draft *roadmapDraft) {
	if draft == nil || len(draft.Nodes) == 0 {
		return
	}

	sort.SliceStable(draft.Nodes, func(left, right int) bool {
		if draft.Nodes[left].OrderIndex == draft.Nodes[right].OrderIndex {
			return draft.Nodes[left].ID < draft.Nodes[right].ID
		}
		return draft.Nodes[left].OrderIndex < draft.Nodes[right].OrderIndex
	})

	const (
		columns       = 3
		horizontalGap = 320.0
		verticalGap   = 170.0
	)
	for index := range draft.Nodes {
		node := &draft.Nodes[index]
		if node.Type == "choice" {
			node.Type = "module"
		}
		node.PathType = "required"
		node.OrderIndex = index
		if index == 0 {
			node.Status = "active"
			node.ParentID = nil
		} else {
			node.Status = "todo"
			parentID := draft.Nodes[index-1].ID
			node.ParentID = &parentID
		}

		row := index / columns
		column := index % columns
		if row%2 == 1 {
			column = columns - 1 - column
		}
		node.X = float64(column) * horizontalGap
		node.Y = float64(row) * verticalGap
	}

	draft.Edges = make([]model.RoadmapEdge, 0, len(draft.Nodes)-1)
	for index := 0; index < len(draft.Nodes)-1; index++ {
		draft.Edges = append(draft.Edges, model.RoadmapEdge{
			ID:           randomLocalID("edge"),
			SourceNodeID: draft.Nodes[index].ID,
			TargetNodeID: draft.Nodes[index+1].ID,
			Style:        "solid",
		})
	}
}

func ensureGeneratedRoadmapDepthAndDetail(draft *roadmapDraft, project model.TaskProject) {
	if draft == nil {
		return
	}

	fallback := mockRoadmapDraft(project)
	if len(draft.Nodes) > 20 {
		draft.Nodes = draft.Nodes[:20]
	}
	for index := range draft.Nodes {
		templateIndex := index
		if templateIndex >= len(fallback.Nodes) {
			templateIndex = len(fallback.Nodes) - 1
		}
		template := fallback.Nodes[templateIndex]
		if strings.TrimSpace(draft.Nodes[index].Description) == "" {
			draft.Nodes[index].Description = template.Description
		}
		if strings.TrimSpace(draft.Nodes[index].Deliverable) == "" {
			draft.Nodes[index].Deliverable = template.Deliverable
		}
		if strings.TrimSpace(draft.Nodes[index].AcceptanceCriteria) == "" {
			draft.Nodes[index].AcceptanceCriteria = template.AcceptanceCriteria
		}
		if len(nonEmptyStrings(draft.Nodes[index].ArticleSearchQueries)) == 0 {
			draft.Nodes[index].ArticleSearchQueries = template.ArticleSearchQueries
		}
	}

	if len(draft.Nodes) >= 14 {
		return
	}
	for index := len(draft.Nodes); index < len(fallback.Nodes); index++ {
		draft.Nodes = append(draft.Nodes, fallback.Nodes[index])
	}
}

func ensureRoadmapBranching(draft *roadmapDraft, project model.TaskProject) {
	if draft == nil || len(draft.Nodes) == 0 {
		return
	}
	if hasRoadmapBranching(draft) {
		return
	}

	source := draft.Nodes[0]
	for _, node := range draft.Nodes {
		if node.Type == "module" || node.Type == "phase" {
			source = node
			break
		}
	}

	leftID := randomLocalID("node")
	rightID := randomLocalID("node")
	baseY := source.Y + 120
	if baseY == 120 {
		baseY = 160
	}
	left := model.RoadmapNode{
		ID:                   leftID,
		Type:                 "choice",
		Title:                "官方文档优先路线",
		Description:          "先读官方文档和权威指南，适合建立准确概念和长期可迁移的理解。",
		PathType:             "recommended",
		Status:               "todo",
		Deliverable:          project.Name + " 官方文档阅读笔记",
		AcceptanceCriteria:   "完成核心章节阅读，记录关键 API、约束和 3 个待验证问题。",
		X:                    source.X - 300,
		Y:                    baseY,
		OrderIndex:           len(draft.Nodes),
		ArticleSearchQueries: []string{project.Name + " official documentation guide", project.Name + " best practices official tutorial"},
	}
	right := model.RoadmapNode{
		ID:                   rightID,
		Type:                 "choice",
		Title:                "项目教程优先路线",
		Description:          "先跟随高质量项目教程跑通一遍，再反向补齐概念和工程细节。",
		PathType:             "alternative",
		Status:               "todo",
		Deliverable:          project.Name + " 可运行教程项目",
		AcceptanceCriteria:   "能独立复现教程核心步骤，并说明每一步解决的问题。",
		X:                    source.X + 300,
		Y:                    baseY,
		OrderIndex:           len(draft.Nodes) + 1,
		ArticleSearchQueries: []string{project.Name + " project based tutorial", project.Name + " hands on project tutorial"},
	}
	draft.Nodes = append(draft.Nodes, left, right)
	draft.Edges = append(draft.Edges,
		model.RoadmapEdge{ID: randomLocalID("edge"), SourceNodeID: source.ID, TargetNodeID: leftID, Style: "dotted"},
		model.RoadmapEdge{ID: randomLocalID("edge"), SourceNodeID: source.ID, TargetNodeID: rightID, Style: "dotted"},
	)
}

func normalizeGeneratedRoadmapLayout(draft *roadmapDraft) {
	if draft == nil || len(draft.Nodes) < 2 {
		return
	}

	separateRoadmapNodeOverlaps(draft.Nodes)
	normalizeRoadmapBranchRows(draft)
	separateRoadmapNodeOverlaps(draft.Nodes)
}

func optimizeRoadmapDraftLayout(draft *roadmapDraft) {
	if draft == nil || len(draft.Nodes) == 0 {
		return
	}
	order := make([]int, len(draft.Nodes))
	for index := range draft.Nodes {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool {
		leftNode := draft.Nodes[order[left]]
		rightNode := draft.Nodes[order[right]]
		if leftNode.OrderIndex != rightNode.OrderIndex {
			return leftNode.OrderIndex < rightNode.OrderIndex
		}
		return leftNode.ID < rightNode.ID
	})
	for row, nodeIndex := range order {
		draft.Nodes[nodeIndex].X = 0
		draft.Nodes[nodeIndex].Y = float64(row * roadmapBranchVerticalGap)
	}

	normalizeRoadmapBranchRows(draft)
	separateRoadmapNodeOverlaps(draft.Nodes)
}

func nextRoadmapNodePlacement(roadmap *model.LearningRoadmap) (int, float64, float64) {
	if roadmap == nil || len(roadmap.Nodes) == 0 {
		return 0, 0, 0
	}
	maxOrder := roadmap.Nodes[0].OrderIndex
	maxY := roadmap.Nodes[0].Y
	for _, node := range roadmap.Nodes {
		if node.OrderIndex > maxOrder {
			maxOrder = node.OrderIndex
		}
		if node.Y > maxY {
			maxY = node.Y
		}
	}
	return maxOrder + 1, 0, maxY + roadmapBranchVerticalGap
}

func childRoadmapNodePlacement(parent model.RoadmapNode, pathType string) (float64, float64) {
	x := parent.X
	switch pathType {
	case "recommended", "optional":
		x = parent.X - roadmapBranchHorizontalGap
	case "alternative":
		x = parent.X + roadmapBranchHorizontalGap
	}
	return x, parent.Y + roadmapBranchVerticalGap
}

func roadmapNodeByID(nodes []model.RoadmapNode, id string) (model.RoadmapNode, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
	}
	return model.RoadmapNode{}, false
}

func normalizeRoadmapNodeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "phase":
		return "phase"
	case "module":
		return "module"
	case "choice":
		return "choice"
	default:
		return "task"
	}
}

func normalizeRoadmapNodePathType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "recommended":
		return "recommended"
	case "optional":
		return "optional"
	case "alternative":
		return "alternative"
	default:
		return "required"
	}
}

func normalizeRoadmapNodeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active":
		return "active"
	case "done":
		return "done"
	case "skipped":
		return "skipped"
	default:
		return "todo"
	}
}

func normalizeRoadmapEdgeStyle(value string, pathType string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "solid":
		return "solid"
	case "dotted":
		return "dotted"
	}
	if pathType == "required" {
		return "solid"
	}
	return "dotted"
}

func separateRoadmapNodeOverlaps(nodes []model.RoadmapNode) {
	maxAttempts := len(nodes) * len(nodes)
	for index := range nodes {
		attempts := 0
		for roadmapNodeOverlapsPrevious(nodes, index) && attempts < maxAttempts {
			nodes[index].Y += roadmapLayoutStepY
			attempts++
		}
	}
}

func normalizeRoadmapBranchRows(draft *roadmapDraft) {
	if len(draft.Edges) < 2 {
		return
	}

	nodeIndexByID := map[string]int{}
	for index, node := range draft.Nodes {
		nodeIndexByID[node.ID] = index
	}

	outgoing := map[string][]model.RoadmapEdge{}
	incoming := map[string][]model.RoadmapEdge{}
	for _, edge := range draft.Edges {
		if _, ok := nodeIndexByID[edge.SourceNodeID]; !ok {
			continue
		}
		if _, ok := nodeIndexByID[edge.TargetNodeID]; !ok {
			continue
		}
		outgoing[edge.SourceNodeID] = append(outgoing[edge.SourceNodeID], edge)
		incoming[edge.TargetNodeID] = append(incoming[edge.TargetNodeID], edge)
	}

	for sourceID, edges := range outgoing {
		if len(edges) < 2 {
			continue
		}
		source := draft.Nodes[nodeIndexByID[sourceID]]
		sort.SliceStable(edges, func(left, right int) bool {
			leftNode := draft.Nodes[nodeIndexByID[edges[left].TargetNodeID]]
			rightNode := draft.Nodes[nodeIndexByID[edges[right].TargetNodeID]]
			if leftRank, rightRank := roadmapBranchTargetRank(leftNode), roadmapBranchTargetRank(rightNode); leftRank != rightRank {
				return leftRank < rightRank
			}
			if leftNode.OrderIndex != rightNode.OrderIndex {
				return leftNode.OrderIndex < rightNode.OrderIndex
			}
			return leftNode.ID < rightNode.ID
		})
		branchY := source.Y + roadmapBranchVerticalGap
		for slot, edge := range edges {
			targetIndex := nodeIndexByID[edge.TargetNodeID]
			draft.Nodes[targetIndex].X = source.X + roadmapBranchSlotOffset(slot, len(edges))*roadmapBranchHorizontalGap
			draft.Nodes[targetIndex].Y = branchY
		}
	}

	for targetID, edges := range incoming {
		if len(edges) < 2 {
			continue
		}
		var sourceXTotal float64
		var maxSourceY float64
		for index, edge := range edges {
			source := draft.Nodes[nodeIndexByID[edge.SourceNodeID]]
			sourceXTotal += source.X
			if index == 0 || source.Y > maxSourceY {
				maxSourceY = source.Y
			}
		}
		targetIndex := nodeIndexByID[targetID]
		draft.Nodes[targetIndex].X = sourceXTotal / float64(len(edges))
		minTargetY := maxSourceY + roadmapBranchVerticalGap
		if draft.Nodes[targetIndex].Y < minTargetY {
			draft.Nodes[targetIndex].Y = minTargetY
		}
	}
}

func roadmapBranchTargetRank(node model.RoadmapNode) int {
	switch node.PathType {
	case "recommended", "optional":
		return 0
	case "required":
		return 1
	case "alternative":
		return 2
	default:
		return 1
	}
}

func roadmapBranchSlotOffset(slot int, total int) float64 {
	if total == 2 {
		if slot == 0 {
			return -1
		}
		return 1
	}
	return float64(slot) - (float64(total)-1)/2
}

func normalizeRoadmapDisplayLayout(roadmap *model.LearningRoadmap) {
	if roadmap == nil || len(roadmap.Nodes) < 2 {
		return
	}
	draft := &roadmapDraft{
		Title: roadmap.Title,
		Goal:  roadmap.Goal,
		Nodes: roadmap.Nodes,
		Edges: roadmap.Edges,
	}
	normalizeGeneratedRoadmapLayout(draft)
	roadmap.Nodes = draft.Nodes
}

func roadmapNodeOverlapsPrevious(nodes []model.RoadmapNode, index int) bool {
	if index <= 0 || index >= len(nodes) {
		return false
	}
	for previous := 0; previous < index; previous++ {
		if roadmapNodesOverlap(nodes[index], nodes[previous]) {
			return true
		}
	}
	return false
}

func roadmapNodesOverlap(left, right model.RoadmapNode) bool {
	return absFloat(left.X-right.X) < roadmapNodeMinGapX && absFloat(left.Y-right.Y) < roadmapNodeMinGapY
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func hasRoadmapBranching(draft *roadmapDraft) bool {
	choiceCount := 0
	for _, node := range draft.Nodes {
		if node.Type == "choice" && (node.PathType == "recommended" || node.PathType == "alternative" || node.PathType == "optional") {
			choiceCount++
		}
	}
	if choiceCount < 2 {
		return false
	}

	fanOut := map[string]int{}
	for _, edge := range draft.Edges {
		if edge.Style == "dotted" {
			fanOut[edge.SourceNodeID]++
		}
	}
	for _, count := range fanOut {
		if count >= 2 {
			return true
		}
	}
	return false
}

func generateOpenAICompatibleRoadmap(project model.TaskProject, customPrompt string) (*roadmapDraft, error) {
	apiKey := strings.TrimSpace(os.Getenv("AI_API_KEY"))
	if apiKey == "" {
		return nil, errors.New("AI_API_KEY is required")
	}
	modelName := strings.TrimSpace(os.Getenv("AI_MODEL"))
	if modelName == "" {
		modelName = defaultAIModel
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("AI_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = defaultAIBaseURL
	}

	body := map[string]interface{}{
		"model":      modelName,
		"max_tokens": 8000,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": buildRoadmapSystemPrompt(),
			},
			{
				"role":    "user",
				"content": buildRoadmapUserPrompt(project, customPrompt),
			},
		},
		"temperature": 0.4,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: defaultAIRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("AI request failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return nil, errors.New("AI response did not include content")
	}

	var draft roadmapDraft
	if err := json.Unmarshal([]byte(decoded.Choices[0].Message.Content), &draft); err != nil {
		return nil, err
	}
	normalizeRoadmapDraftIDs(&draft)
	if strings.TrimSpace(draft.Title) == "" {
		draft.Title = project.Name + " 学习路线"
	}
	if strings.TrimSpace(draft.Goal) == "" {
		draft.Goal = "通过项目实战掌握 " + project.Name
	}
	if len(draft.Nodes) == 0 {
		return nil, errors.New("AI response did not include roadmap nodes")
	}
	return &draft, nil
}

func normalizeRoadmapDraftIDs(draft *roadmapDraft) {
	idMap := map[string]string{}
	for index := range draft.Nodes {
		oldID := draft.Nodes[index].ID
		newID := randomLocalID("node")
		idMap[oldID] = newID
		draft.Nodes[index].ID = newID
		if draft.Nodes[index].OrderIndex == 0 {
			draft.Nodes[index].OrderIndex = index
		}
	}
	for index := range draft.Edges {
		if source, ok := idMap[draft.Edges[index].SourceNodeID]; ok {
			draft.Edges[index].SourceNodeID = source
		}
		if target, ok := idMap[draft.Edges[index].TargetNodeID]; ok {
			draft.Edges[index].TargetNodeID = target
		}
		draft.Edges[index].ID = randomLocalID("edge")
	}
}

func sanitizeGeneratedRoadmapDraft(draft *roadmapDraft) {
	if draft == nil {
		return
	}

	nodeIDs := make(map[string]bool, len(draft.Nodes))
	for index := range draft.Nodes {
		node := &draft.Nodes[index]
		node.ID = strings.TrimSpace(node.ID)
		if node.ID == "" || nodeIDs[node.ID] {
			node.ID = randomLocalID("node")
		}
		nodeIDs[node.ID] = true

		node.Type = normalizeRoadmapNodeType(node.Type)
		node.PathType = normalizeRoadmapNodePathType(node.PathType)
		node.Status = normalizeRoadmapNodeStatus(node.Status)
		node.Title = strings.TrimSpace(node.Title)
		if node.Title == "" {
			node.Title = fmt.Sprintf("Learning node %d", index+1)
		}
		node.Description = strings.TrimSpace(node.Description)
		node.Deliverable = strings.TrimSpace(node.Deliverable)
		node.AcceptanceCriteria = strings.TrimSpace(node.AcceptanceCriteria)
		if (index > 0 && node.OrderIndex == 0) || node.OrderIndex < 0 {
			node.OrderIndex = index
		}
		node.ArticleSearchQueries = nonEmptyStrings(node.ArticleSearchQueries)
	}

	nodesByID := make(map[string]model.RoadmapNode, len(draft.Nodes))
	for _, node := range draft.Nodes {
		nodesByID[node.ID] = node
	}
	for index := range draft.Nodes {
		if draft.Nodes[index].ParentID == nil {
			continue
		}
		parentID := strings.TrimSpace(*draft.Nodes[index].ParentID)
		if parentID == "" || parentID == draft.Nodes[index].ID || !nodeIDs[parentID] {
			draft.Nodes[index].ParentID = nil
			continue
		}
		draft.Nodes[index].ParentID = &parentID
	}

	edges := make([]model.RoadmapEdge, 0, len(draft.Edges))
	seenEdges := map[string]bool{}
	edgeIDs := map[string]bool{}
	for _, edge := range draft.Edges {
		edge.SourceNodeID = strings.TrimSpace(edge.SourceNodeID)
		edge.TargetNodeID = strings.TrimSpace(edge.TargetNodeID)
		if edge.SourceNodeID == "" || edge.TargetNodeID == "" || edge.SourceNodeID == edge.TargetNodeID {
			continue
		}
		targetNode, targetOK := nodesByID[edge.TargetNodeID]
		if !nodeIDs[edge.SourceNodeID] || !targetOK {
			continue
		}
		key := edge.SourceNodeID + "->" + edge.TargetNodeID
		if seenEdges[key] {
			continue
		}
		seenEdges[key] = true

		edge.ID = strings.TrimSpace(edge.ID)
		if edge.ID == "" || edgeIDs[edge.ID] {
			edge.ID = randomLocalID("edge")
		}
		edgeIDs[edge.ID] = true
		edge.Style = normalizeRoadmapEdgeStyle(edge.Style, targetNode.PathType)
		edges = append(edges, edge)
	}
	if len(edges) == 0 && len(draft.Nodes) > 1 {
		for index := 1; index < len(draft.Nodes); index++ {
			source := draft.Nodes[index-1]
			target := draft.Nodes[index]
			edges = append(edges, model.RoadmapEdge{
				ID:           randomLocalID("edge"),
				SourceNodeID: source.ID,
				TargetNodeID: target.ID,
				Style:        normalizeRoadmapEdgeStyle("", target.PathType),
			})
		}
	}
	draft.Edges = edges
}

func legacySearchArticleResources(node model.RoadmapNode) ([]model.RoadmapResource, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_PROVIDER")))
	if provider == "none" {
		return []model.RoadmapResource{}, nil
	}
	if provider == "tavily" && strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_API_KEY")) != "" {
		return searchTavilyResources(node)
	}
	return []model.RoadmapResource{
		{
			Title:   node.Title + " 官方文档",
			URL:     "https://example.com/search?q=" + strings.ReplaceAll(node.Title, " ", "+"),
			Summary: "用于占位和本地验证的 mock 技术文章链接。",
		},
		{
			Title:   node.Title + " 项目实战教程",
			URL:     "https://example.com/tutorial?q=" + strings.ReplaceAll(node.Title, " ", "+"),
			Summary: "围绕该节点目标的项目实战参考。",
		},
	}, nil
}

func searchTavilyResources(node model.RoadmapNode) ([]model.RoadmapResource, error) {
	maxResults := 3
	if parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_MAX_RESULTS"))); err == nil && parsed > 0 && parsed <= 10 {
		maxResults = parsed
	}
	payload, err := json.Marshal(map[string]interface{}{
		"api_key":     strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_API_KEY")),
		"query":       node.Title + " technical article tutorial official docs",
		"max_results": maxResults,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("article search failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var decoded struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	resources := make([]model.RoadmapResource, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		if strings.TrimSpace(result.URL) == "" {
			continue
		}
		resources = append(resources, model.RoadmapResource{
			Title:   strings.TrimSpace(result.Title),
			URL:     strings.TrimSpace(result.URL),
			Summary: strings.TrimSpace(result.Content),
		})
	}
	return resources, nil
}

func searchArticleResources(node model.RoadmapNode, linkedTasks []model.Task, requestedSources []string, limit int) ([]model.RoadmapResource, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_PROVIDER")))
	if provider == "" {
		provider = defaultArticleProvider
	}
	sources := selectedArticleSearchSources(requestedSources)
	limit = normalizeArticleSearchLimit(limit)
	if provider == "none" {
		return []model.RoadmapResource{}, nil
	}
	if provider == "mock" {
		return mockArticleResources(node, linkedTasks, sources, limit), nil
	}
	if provider == "tavily" {
		if strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_API_KEY")) == "" {
			return nil, errors.New("ARTICLE_SEARCH_API_KEY is required for tavily")
		}
		return searchTavilyResourcesForNode(node, linkedTasks, sources, limit)
	}
	publicResources := searchPublicSourceResources(node, linkedTasks, sources, limit)
	if len(publicResources) >= limit {
		return publicResources, nil
	}
	bingResources, bingErr := searchBingResources(node, linkedTasks, sources, limit-len(publicResources))
	combined := filterAndRankArticleResources(node, linkedTasks, append(publicResources, bingResources...), limit)
	if len(combined) >= limit {
		return combined, nil
	}
	duckDuckGoResources, duckDuckGoErr := searchDuckDuckGoResources(node, linkedTasks, sources, limit-len(combined))
	combined = filterAndRankArticleResources(node, linkedTasks, append(combined, duckDuckGoResources...), limit)
	if len(combined) > 0 {
		return combined, nil
	}
	if bingErr != nil && duckDuckGoErr != nil {
		return nil, fmt.Errorf("article search providers failed: bing: %v; duckduckgo: %v", bingErr, duckDuckGoErr)
	}
	return []model.RoadmapResource{}, nil
}

func searchPublicSourceResources(node model.RoadmapNode, linkedTasks []model.Task, sources []articleSearchSource, limit int) []model.RoadmapResource {
	limit = normalizeArticleSearchLimit(limit)
	resources := make([]model.RoadmapResource, 0, limit)
	for _, source := range sources {
		switch source.ID {
		case "devto":
			resources = append(resources, searchDevToResources(node, linkedTasks, limit)...)
		case "stackoverflow":
			resources = append(resources, searchStackOverflowResources(node, linkedTasks, limit)...)
		}
	}
	return filterAndRankArticleResources(node, linkedTasks, resources, limit)
}

func searchDevToResources(node model.RoadmapNode, linkedTasks []model.Task, limit int) []model.RoadmapResource {
	limit = normalizeArticleSearchLimit(limit)
	tag, ok := articleTopicTag(node, linkedTasks)
	if !ok {
		return nil
	}
	values := url.Values{}
	values.Set("tag", tag)
	values.Set("top", "365")
	values.Set("per_page", strconv.Itoa(limit))
	requestURL := devToArticlesURL + "?" + values.Encode()

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "FlowSpaceRoadmap/1.0")
	resp, err := articleHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil
	}

	var decoded []struct {
		Title                string `json:"title"`
		URL                  string `json:"url"`
		Description          string `json:"description"`
		PublicReactionsCount int    `json:"public_reactions_count"`
		ReadingTimeMinutes   int    `json:"reading_time_minutes"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1_000_000)).Decode(&decoded); err != nil {
		return nil
	}

	resources := make([]model.RoadmapResource, 0, len(decoded))
	for _, article := range decoded {
		title := strings.TrimSpace(article.Title)
		link := strings.TrimSpace(article.URL)
		if title == "" || link == "" {
			continue
		}
		summaryParts := []string{fmt.Sprintf("Dev.to 热门文章 · %d reactions", article.PublicReactionsCount)}
		if article.ReadingTimeMinutes > 0 {
			summaryParts = append(summaryParts, fmt.Sprintf("%d min read", article.ReadingTimeMinutes))
		}
		if description := strings.TrimSpace(article.Description); description != "" {
			summaryParts = append(summaryParts, description)
		}
		resources = append(resources, model.RoadmapResource{
			Title:   title,
			URL:     link,
			Summary: strings.Join(summaryParts, " · "),
		})
	}
	return resources
}

func searchStackOverflowResources(node model.RoadmapNode, linkedTasks []model.Task, limit int) []model.RoadmapResource {
	limit = normalizeArticleSearchLimit(limit)
	values := url.Values{}
	values.Set("order", "desc")
	values.Set("sort", "votes")
	values.Set("q", baseArticleSearchQuery(node, linkedTasks))
	values.Set("site", "stackoverflow")
	values.Set("pagesize", strconv.Itoa(limit))
	requestURL := stackExchangeSearchURL + "?" + values.Encode()

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "FlowSpaceRoadmap/1.0")
	resp, err := articleHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil
	}

	var decoded struct {
		Items []struct {
			Title       string `json:"title"`
			Link        string `json:"link"`
			Score       int    `json:"score"`
			AnswerCount int    `json:"answer_count"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1_000_000)).Decode(&decoded); err != nil {
		return nil
	}

	resources := make([]model.RoadmapResource, 0, len(decoded.Items))
	for _, item := range decoded.Items {
		title := strings.TrimSpace(stdhtml.UnescapeString(item.Title))
		link := strings.TrimSpace(item.Link)
		if title == "" || link == "" {
			continue
		}
		resources = append(resources, model.RoadmapResource{
			Title:   title,
			URL:     link,
			Summary: fmt.Sprintf("Stack Overflow 高票讨论 · %d votes · %d answers", item.Score, item.AnswerCount),
		})
	}
	return resources
}

func articleTopicTag(node model.RoadmapNode, linkedTasks []model.Task) (string, bool) {
	text := strings.ToLower(strings.Join(nonEmptyStrings([]string{
		node.Title,
		node.Deliverable,
		strings.Join(node.ArticleSearchQueries, " "),
		linkedTaskArticleSearchContext(linkedTasks),
	}), " "))
	switch {
	case strings.Contains(text, "golang") || strings.Contains(text, " go ") || strings.HasPrefix(text, "go "):
		return "go", true
	case strings.Contains(text, "python"):
		return "python", true
	case strings.Contains(text, "docker") || strings.Contains(text, "container"):
		return "docker", true
	case strings.Contains(text, "kubernetes") || strings.Contains(text, "k8s"):
		return "kubernetes", true
	case strings.Contains(text, "react"):
		return "react", true
	case strings.Contains(text, "typescript"):
		return "typescript", true
	case strings.Contains(text, "javascript"):
		return "javascript", true
	case strings.Contains(text, "postgres") || strings.Contains(text, "database") || strings.Contains(text, "sql"):
		return "database", true
	case strings.Contains(text, "devops") || strings.Contains(text, "infra") || strings.Contains(text, "deploy"):
		return "devops", true
	case strings.Contains(text, "ai") || strings.Contains(text, "llm") || strings.Contains(text, "model") || strings.Contains(text, "gpu"):
		return "ai", true
	default:
		return "", false
	}
}

func mockArticleResources(node model.RoadmapNode, linkedTasks []model.Task, sources []articleSearchSource, limit int) []model.RoadmapResource {
	if len(sources) == 0 {
		sources = selectedArticleSearchSources(nil)
	}
	searchQuery := baseArticleSearchQuery(node, linkedTasks)
	titleSuffixes := []string{
		"官方文档",
		"项目实战教程",
		"深度技术文章",
		"踩坑经验",
		"社区讨论",
		"最佳实践",
		"案例复盘",
		"架构笔记",
		"入门指南",
		"进阶练习",
	}
	resources := make([]model.RoadmapResource, 0, limit)
	for index := 0; index < limit; index++ {
		source := sources[index%len(sources)]
		query := url.Values{"q": []string{searchQuery}}.Encode()
		separator := "?"
		if strings.Contains(source.MockURL, "?") {
			separator = "&"
		}
		resources = append(resources, model.RoadmapResource{
			Title:   node.Title + " " + titleSuffixes[index%len(titleSuffixes)],
			URL:     source.MockURL + separator + query,
			Summary: source.Label + " 搜索结果，用于本地验证的 mock 技术文章链接。",
		})
	}
	return resources
}

func searchTavilyResourcesForNode(node model.RoadmapNode, linkedTasks []model.Task, sources []articleSearchSource, limit int) ([]model.RoadmapResource, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"api_key":     strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_API_KEY")),
		"query":       buildArticleSearchQuery(node, linkedTasks, sources),
		"max_results": limit,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("article search failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var decoded struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	resources := make([]model.RoadmapResource, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		if strings.TrimSpace(result.URL) == "" {
			continue
		}
		resources = append(resources, model.RoadmapResource{
			Title:   strings.TrimSpace(result.Title),
			URL:     strings.TrimSpace(result.URL),
			Summary: strings.TrimSpace(result.Content),
		})
	}
	return filterAndRankArticleResources(node, linkedTasks, resources, limit), nil
}

func searchBingResources(node model.RoadmapNode, linkedTasks []model.Task, sources []articleSearchSource, limit int) ([]model.RoadmapResource, error) {
	limit = normalizeArticleSearchLimit(limit)
	values := url.Values{}
	values.Set("format", "rss")
	values.Set("q", buildArticleSearchQuery(node, linkedTasks, sources))
	requestURL := bingSearchURL + "?" + values.Encode()

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 FlowSpaceRoadmap/1.0")
	resp, err := articleHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Bing article search failed: %s", resp.Status)
	}

	var decoded struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1_000_000)).Decode(&decoded); err != nil {
		return nil, err
	}

	resources := make([]model.RoadmapResource, 0, len(decoded.Channel.Items))
	for _, item := range decoded.Channel.Items {
		title := strings.TrimSpace(stdhtml.UnescapeString(item.Title))
		link := strings.TrimSpace(item.Link)
		if title == "" || link == "" {
			continue
		}
		resources = append(resources, model.RoadmapResource{
			Title:   title,
			URL:     link,
			Summary: strings.TrimSpace(stdhtml.UnescapeString(item.Description)),
		})
	}
	return filterAndRankArticleResources(node, linkedTasks, resources, limit), nil
}

func searchDuckDuckGoResources(node model.RoadmapNode, linkedTasks []model.Task, sources []articleSearchSource, limit int) ([]model.RoadmapResource, error) {
	searchURL := "https://duckduckgo.com/html/?" + url.Values{"q": []string{buildArticleSearchQuery(node, linkedTasks, sources)}}.Encode()
	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 FlowSpaceRoadmap/1.0")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("article search failed: %s", resp.Status)
	}

	doc, err := html.Parse(io.LimitReader(resp.Body, 1_000_000))
	if err != nil {
		return nil, err
	}
	return filterAndRankArticleResources(node, linkedTasks, extractDuckDuckGoResults(doc, limit), limit), nil
}

func buildArticleSearchQuery(node model.RoadmapNode, linkedTasks []model.Task, sources []articleSearchSource) string {
	return compactArticleSearchQuery(strings.Join(nonEmptyStrings([]string{baseArticleSearchQuery(node, linkedTasks), highSignalArticleHint, articleSearchSourceQuery(sources)}), " "))
}

func baseArticleSearchQuery(node model.RoadmapNode, linkedTasks []model.Task) string {
	primaryQuery := ""
	for _, query := range node.ArticleSearchQueries {
		if trimmed := strings.TrimSpace(query); trimmed != "" {
			primaryQuery = trimmed
			break
		}
	}
	taskContext := linkedTaskArticleSearchContext(linkedTasks)
	return compactArticleSearchQuery(strings.Join(nonEmptyStrings([]string{
		strings.Join(articleTopicAliases(node, linkedTasks), " "),
		primaryQuery,
		node.Title,
		node.Description,
		node.Deliverable,
		taskContext,
		"official documentation tutorial",
	}), " "))
}

func linkedTaskArticleSearchContext(tasks []model.Task) string {
	parts := make([]string, 0, len(tasks)*2)
	for _, task := range tasks {
		parts = append(parts, task.Title, task.Content)
	}
	return compactArticleSearchQuery(strings.Join(nonEmptyStrings(parts), " "))
}

func compactArticleSearchQuery(raw string) string {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return ""
	}
	const maxLength = 280
	compacted := make([]string, 0, len(fields))
	currentLength := 0
	for _, field := range fields {
		nextLength := len(field)
		if currentLength > 0 {
			nextLength++
		}
		if currentLength+nextLength > maxLength {
			break
		}
		compacted = append(compacted, field)
		currentLength += nextLength
	}
	return strings.Join(compacted, " ")
}

func selectedArticleSearchSources(requested []string) []articleSearchSource {
	sourceIDs := nonEmptyStrings(requested)
	if len(sourceIDs) == 0 {
		sourceIDs = nonEmptyStrings(strings.Split(os.Getenv("ARTICLE_SEARCH_SOURCES"), ","))
	}
	if len(sourceIDs) == 0 {
		for _, source := range articleSearchSourceCatalog {
			sourceIDs = append(sourceIDs, source.ID)
		}
	}

	catalog := make(map[string]articleSearchSource, len(articleSearchSourceCatalog))
	for _, source := range articleSearchSourceCatalog {
		catalog[source.ID] = source
	}
	selected := make([]articleSearchSource, 0, len(sourceIDs))
	seen := map[string]bool{}
	for _, rawID := range sourceIDs {
		id := strings.ToLower(strings.TrimSpace(rawID))
		if id == "stack-overflow" {
			id = "stackoverflow"
		}
		if source, ok := catalog[id]; ok && !seen[source.ID] {
			selected = append(selected, source)
			seen[source.ID] = true
			continue
		}
		if source, ok := customArticleSearchSource(id); ok && !seen[source.ID] {
			selected = append(selected, source)
			seen[source.ID] = true
		}
	}
	if len(selected) == 0 {
		return articleSearchSourceCatalog
	}
	return selected
}

func customArticleSearchSource(rawID string) (articleSearchSource, bool) {
	if !strings.HasPrefix(rawID, "site:") {
		return articleSearchSource{}, false
	}
	domain := strings.TrimSpace(strings.TrimPrefix(rawID, "site:"))
	parsed, err := url.Parse("https://" + domain)
	if err != nil || parsed.Hostname() == "" || parsed.Host != domain || parsed.Path != "" {
		return articleSearchSource{}, false
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if hostname == "" || !strings.Contains(hostname, ".") {
		return articleSearchSource{}, false
	}
	id := "site:" + hostname
	return articleSearchSource{ID: id, Label: hostname, QueryHint: id, MockURL: "https://" + hostname}, true
}

func articleSearchSourceQuery(sources []articleSearchSource) string {
	if len(sources) == 0 {
		sources = selectedArticleSearchSources(nil)
	}
	hints := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.ID == "google" {
			continue
		}
		if strings.TrimSpace(source.QueryHint) != "" {
			hints = append(hints, source.QueryHint)
		}
	}
	if len(hints) == 1 {
		return hints[0]
	}
	if len(hints) == 0 {
		return ""
	}
	return "(" + strings.Join(hints, " OR ") + ")"
}

func articleSearchMaxResults() int {
	if parsed, err := strconv.Atoi(strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_MAX_RESULTS"))); err == nil && parsed > 0 {
		return normalizeManualArticleSearchLimit(parsed)
	}
	return minArticleSearchResults
}

func normalizeArticleSearchLimit(limit int) int {
	if limit <= 0 {
		return minArticleSearchResults
	}
	if limit > maxArticleSearchResults {
		return maxArticleSearchResults
	}
	return limit
}

func normalizeManualArticleSearchLimit(limit int) int {
	normalized := normalizeArticleSearchLimit(limit)
	if normalized < minArticleSearchResults {
		return minArticleSearchResults
	}
	return normalized
}

func limitArticleResources(resources []model.RoadmapResource, limit int) []model.RoadmapResource {
	limit = normalizeArticleSearchLimit(limit)
	limited := make([]model.RoadmapResource, 0, limit)
	seen := map[string]bool{}
	for _, resource := range resources {
		resource.URL = strings.TrimSpace(resource.URL)
		resource.Title = strings.TrimSpace(resource.Title)
		resource.Summary = strings.TrimSpace(resource.Summary)
		if resource.URL == "" || seen[resource.URL] || isArticleSearchEntry(resource) {
			continue
		}
		if resource.Title == "" {
			resource.Title = resource.URL
		}
		seen[resource.URL] = true
		limited = append(limited, resource)
		if len(limited) >= limit {
			return limited
		}
	}
	return limited
}

func ensureArticleResourceChoices(node model.RoadmapNode, sources []articleSearchSource, resources []model.RoadmapResource, limit int) []model.RoadmapResource {
	return filterAndRankArticleResources(node, nil, resources, limit)
}

type scoredArticleResource struct {
	resource model.RoadmapResource
	score    int
	index    int
}

func filterAndRankArticleResources(node model.RoadmapNode, linkedTasks []model.Task, resources []model.RoadmapResource, limit int) []model.RoadmapResource {
	limit = normalizeArticleSearchLimit(limit)
	keywords := articleRelevanceKeywords(node, linkedTasks)
	if len(keywords) == 0 {
		return []model.RoadmapResource{}
	}

	unique := limitArticleResources(resources, maxArticleSearchResults)
	scored := make([]scoredArticleResource, 0, len(unique))
	for index, resource := range unique {
		score := articleResourceRelevanceScore(resource, keywords)
		if score < 2 || !matchesRequiredArticleTopics(node, linkedTasks, resource) {
			continue
		}
		scored = append(scored, scoredArticleResource{resource: resource, score: score, index: index})
	}
	sort.SliceStable(scored, func(left, right int) bool {
		if scored[left].score == scored[right].score {
			return scored[left].index < scored[right].index
		}
		return scored[left].score > scored[right].score
	})

	filtered := make([]model.RoadmapResource, 0, minInt(limit, len(scored)))
	for _, candidate := range scored {
		filtered = append(filtered, candidate.resource)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func articleRelevanceKeywords(node model.RoadmapNode, linkedTasks []model.Task) []string {
	contextParts := articleTopicAliases(node, linkedTasks)
	contextParts = append(contextParts, node.Title, node.Description, node.Deliverable, node.AcceptanceCriteria)
	contextParts = append(contextParts, node.ArticleSearchQueries...)
	contextParts = append(contextParts, linkedTaskArticleSearchContext(linkedTasks))

	stopWords := map[string]bool{
		"a": true, "an": true, "and": true, "article": true, "articles": true, "basics": true,
		"best": true, "complete": true, "documentation": true, "for": true, "foundation": true,
		"guide": true, "high": true, "how": true, "introduction": true, "learn": true,
		"learning": true, "most": true, "official": true, "overview": true, "phase": true,
		"plan": true, "popular": true, "practice": true, "quality": true, "read": true,
		"recommended": true, "stage": true, "study": true, "the": true, "to": true,
		"tutorial": true, "upvoted": true, "with": true,
	}

	seen := map[string]bool{}
	keywords := make([]string, 0, 24)
	for _, field := range strings.FieldsFunc(strings.ToLower(strings.Join(nonEmptyStrings(contextParts), " ")), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 127)
	}) {
		term := strings.TrimSpace(field)
		if term == "" || stopWords[term] || seen[term] {
			continue
		}
		if len([]rune(term)) < 3 && !strings.ContainsAny(term, "0123456789") && term != "go" && term != "ai" {
			continue
		}
		seen[term] = true
		keywords = append(keywords, term)
		if len(keywords) >= 24 {
			break
		}
	}
	return keywords
}

func articleTopicAliases(node model.RoadmapNode, linkedTasks []model.Task) []string {
	contextText := articleSearchContextText(node, linkedTasks)
	if !isJapaneseLearningContext(contextText) {
		return nil
	}

	aliases := []string{"Japanese language"}
	for _, level := range []string{"n1", "n2", "n3", "n4", "n5"} {
		if strings.Contains(contextText, level) {
			aliases = append([]string{"JLPT " + strings.ToUpper(level)}, aliases...)
			break
		}
	}
	return aliases
}

func matchesRequiredArticleTopics(node model.RoadmapNode, linkedTasks []model.Task, resource model.RoadmapResource) bool {
	contextText := articleSearchContextText(node, linkedTasks)
	if !isJapaneseLearningContext(contextText) {
		return true
	}

	level := ""
	for _, candidate := range []string{"n1", "n2", "n3", "n4", "n5"} {
		if strings.Contains(contextText, candidate) {
			level = candidate
			break
		}
	}
	if level == "" {
		return true
	}

	resourceText := strings.ToLower(strings.Join([]string{resource.Title, resource.Summary, resource.URL}, " "))
	return strings.Contains(resourceText, level) ||
		strings.Contains(resourceText, "jlpt") ||
		strings.Contains(resourceText, "日本语能力测试") ||
		strings.Contains(resourceText, "日本語能力試験")
}

func articleSearchContextText(node model.RoadmapNode, linkedTasks []model.Task) string {
	parts := []string{node.Title, node.Description, node.Deliverable, node.AcceptanceCriteria}
	parts = append(parts, node.ArticleSearchQueries...)
	parts = append(parts, linkedTaskArticleSearchContext(linkedTasks))
	return strings.ToLower(strings.Join(nonEmptyStrings(parts), " "))
}

func isJapaneseLearningContext(contextText string) bool {
	return strings.Contains(contextText, "日语") ||
		strings.Contains(contextText, "日語") ||
		strings.Contains(contextText, "日本語") ||
		strings.Contains(contextText, "japanese") ||
		strings.Contains(contextText, "jlpt")
}

func articleResourceRelevanceScore(resource model.RoadmapResource, keywords []string) int {
	title := strings.ToLower(strings.TrimSpace(resource.Title))
	summary := strings.ToLower(strings.TrimSpace(resource.Summary))
	resourceURL := strings.ToLower(strings.TrimSpace(resource.URL))
	score := 0
	for _, keyword := range keywords {
		if strings.Contains(title, keyword) {
			score += 5
		}
		if strings.Contains(summary, keyword) {
			score += 2
		}
		if strings.Contains(resourceURL, keyword) {
			score++
		}
	}
	return score
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func isArticleSearchEntry(resource model.RoadmapResource) bool {
	lowerTitle := strings.ToLower(resource.Title)
	lowerSummary := strings.ToLower(resource.Summary)
	if strings.Contains(resource.Title, "搜索入口") ||
		strings.Contains(resource.Summary, "搜索入口") ||
		strings.Contains(lowerTitle, "search entry") ||
		strings.Contains(lowerSummary, "search entry") {
		return true
	}

	parsed, err := url.Parse(resource.URL)
	if err != nil {
		return false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	path := strings.TrimRight(strings.ToLower(parsed.EscapedPath()), "/")
	if path == "" {
		path = "/"
	}

	switch host {
	case "google.com":
		return path == "/search"
	case "duckduckgo.com":
		return path == "/" || path == "/html"
	case "medium.com":
		return path == "/search"
	case "reddit.com":
		return path == "/search"
	case "dev.to":
		return path == "/search"
	case "hashnode.com":
		return path == "/search"
	case "stackoverflow.com":
		return path == "/search"
	case "github.com":
		return path == "/search"
	case "web.dev":
		return path == "/s/results"
	default:
		return false
	}
}

func extractDuckDuckGoResults(root *html.Node, limit int) []model.RoadmapResource {
	results := make([]model.RoadmapResource, 0, limit)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil || len(results) >= limit {
			return
		}
		if node.Type == html.ElementNode && node.Data == "a" && hasHTMLClass(node, "result__a") {
			title := strings.TrimSpace(nodeText(node))
			link := normalizeDuckDuckGoHref(attrValue(node, "href"))
			if title != "" && link != "" {
				results = append(results, model.RoadmapResource{
					Title:   title,
					URL:     link,
					Summary: "在线搜索结果",
				})
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return results
}

func hasHTMLClass(node *html.Node, className string) bool {
	for _, attr := range node.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, part := range strings.Fields(attr.Val) {
			if part == className {
				return true
			}
		}
	}
	return false
}

func attrValue(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func normalizeDuckDuckGoHref(href string) string {
	if strings.TrimSpace(href) == "" {
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil {
		return href
	}
	if parsed.IsAbs() && !strings.Contains(parsed.Host, "duckduckgo.com") {
		return parsed.String()
	}
	if encoded := parsed.Query().Get("uddg"); encoded != "" {
		if decoded, err := url.QueryUnescape(encoded); err == nil {
			return decoded
		}
		return encoded
	}
	if strings.HasPrefix(href, "//") {
		return "https:" + href
	}
	return href
}

func nodeText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func randomLocalID(prefix string) string {
	var bytes [6]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(bytes[:])
}
