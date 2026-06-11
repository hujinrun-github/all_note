package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/repository"
	"golang.org/x/net/html"
)

type roadmapDraft struct {
	Title string              `json:"title"`
	Goal  string              `json:"goal"`
	Nodes []model.RoadmapNode `json:"nodes"`
	Edges []model.RoadmapEdge `json:"edges"`
}

const (
	defaultAIProvider       = "deepseek"
	defaultAIBaseURL        = "https://api.deepseek.com"
	defaultAIModel          = "deepseek-v4-pro"
	defaultAIRequestTimeout = 120 * time.Second
	defaultArticleProvider  = "duckduckgo"
	minArticleSearchResults = 10
	maxArticleSearchResults = 20
	autoResourceNodeLimit   = 6
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

func GenerateLearningRoadmap(projectID string) (*model.LearningRoadmap, error) {
	project, err := repository.GetTaskProjectByID(projectID)
	if err != nil {
		return nil, err
	}
	if project.Type != "learning" {
		return nil, fmt.Errorf("project is not a learning project")
	}

	draft, err := generateRoadmapDraft(*project)
	if err != nil {
		_, _ = repository.SaveFailedLearningRoadmap(project.ID, project.Name+" 学习路线", project.Description)
		return nil, err
	}

	ensureRoadmapBranching(draft, *project)

	roadmap := &model.LearningRoadmap{
		ProjectID: project.ID,
		Title:     draft.Title,
		Goal:      draft.Goal,
		Status:    "ready",
		Nodes:     draft.Nodes,
		Edges:     draft.Edges,
	}
	saved, err := repository.ReplaceLearningRoadmap(roadmap)
	if err != nil {
		return nil, err
	}
	if err := attachInitialRoadmapResources(draft); err != nil {
		return saved, nil
	}
	withResources, err := repository.GetLearningRoadmap(project.ID)
	if err != nil {
		return saved, nil
	}
	return withResources, nil
}

func GetLearningRoadmap(projectID string) (*model.LearningRoadmap, error) {
	return repository.GetLearningRoadmap(projectID)
}

func UpdateRoadmapNode(id string, req *model.UpdateRoadmapNodeRequest) (*model.RoadmapNode, error) {
	return repository.UpdateRoadmapNode(id, req)
}

func UpdateRoadmapLayout(roadmapID string, nodes []model.RoadmapLayoutNode) error {
	return repository.UpdateRoadmapLayout(roadmapID, nodes)
}

func SearchRoadmapNodeResources(nodeID string, req *model.SearchRoadmapResourcesRequest) ([]model.RoadmapResource, error) {
	node, err := repository.GetRoadmapNode(nodeID)
	if err != nil {
		return nil, err
	}

	var sources []string
	if req != nil {
		sources = req.Sources
	}
	results, err := searchArticleResources(*node, sources, articleSearchMaxResults())
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
	return results, nil
}

func AddRoadmapNodeResource(nodeID string, req *model.CreateRoadmapResourceRequest) (*model.RoadmapResource, error) {
	if _, err := repository.GetRoadmapNode(nodeID); err != nil {
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
	if err := repository.AddRoadmapResource(resource); err != nil {
		return nil, err
	}
	return resource, nil
}

func DeleteRoadmapResource(id string) error {
	return repository.DeleteRoadmapResource(id)
}

func generateRoadmapDraft(project model.TaskProject) (*roadmapDraft, error) {
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
	return generateOpenAICompatibleRoadmap(project)
}

func mockRoadmapDraft(project model.TaskProject) *roadmapDraft {
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

func buildRoadmapSystemPrompt() string {
	return strings.Join([]string{
		"Return only valid JSON and no markdown.",
		"Build a project-practice learning roadmap as graph data, similar to roadmap.sh but tailored for execution inside a task app.",
		"The roadmap must not be a linear checklist. It must include branch learning logic:",
		"- one main required path for the recommended learning order",
		"- at least two choice branch nodes from the same parent node",
		"- branch nodes must use type=choice and path_type=recommended, alternative, or optional",
		"- branch edges must use style=dotted, while the main path uses style=solid",
		"- branches should represent meaningful learning decisions such as official-docs-first, project-tutorial-first, framework choice, or depth-vs-speed path",
		"Each phase/module/task must be project-practice oriented and include a concrete deliverable and acceptance_criteria.",
		"Each node must include article_search_queries with 2 or 3 English search queries for online articles, official documentation, and high-quality tutorials.",
		"Do not invent article URLs. Provide search queries only; the backend will query online articles and attach real links.",
		"Use Chinese titles and descriptions.",
		"Node schema: id,type,title,description,path_type,status,deliverable,acceptance_criteria,x,y,order_index,article_search_queries.",
		"Allowed node.type values: phase,module,task,choice.",
		"Allowed node.path_type values: required,recommended,optional,alternative.",
		"Allowed node.status values: active,todo.",
		"Edge schema: id,source_node_id,target_node_id,style.",
		"Allowed edge.style values: solid,dotted.",
		"Return JSON object schema: {\"title\":\"...\",\"goal\":\"...\",\"nodes\":[...],\"edges\":[...]}.",
	}, "\n")
}

func buildRoadmapUserPrompt(project model.TaskProject) string {
	return fmt.Sprintf(strings.Join([]string{
		"Project name: %s",
		"Description: %s",
		"Generate a concise but branch-aware project-practice learning roadmap.",
		"Prefer 6 to 10 nodes.",
		"Make the central path directly lead to a runnable project deliverable.",
		"Make the branch paths useful choices, not decorative side notes.",
		"Give x/y positions so the main path is vertical and branches spread left/right.",
	}, "\n"), project.Name, project.Description)
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

func attachInitialRoadmapResources(draft *roadmapDraft) error {
	if draft == nil {
		return nil
	}
	attachedNodes := 0
	for _, node := range draft.Nodes {
		if attachedNodes >= autoResourceNodeLimit {
			return nil
		}
		resources, err := searchArticleResources(node, nil, 2)
		if err != nil {
			continue
		}
		for index := range resources {
			resources[index].NodeID = node.ID
			resources[index].SourceType = "article"
			resources[index].AddedBy = "search"
			if err := repository.AddRoadmapResource(&resources[index]); err != nil {
				return err
			}
		}
		if len(resources) > 0 {
			attachedNodes++
		}
	}
	return nil
}

func generateOpenAICompatibleRoadmap(project model.TaskProject) (*roadmapDraft, error) {
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
		"model": modelName,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": buildRoadmapSystemPrompt(),
			},
			{
				"role":    "user",
				"content": buildRoadmapUserPrompt(project),
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

func searchArticleResources(node model.RoadmapNode, requestedSources []string, limit int) ([]model.RoadmapResource, error) {
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
		return mockArticleResources(node, sources, limit), nil
	}
	if provider == "tavily" {
		if strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_API_KEY")) == "" {
			return nil, errors.New("ARTICLE_SEARCH_API_KEY is required for tavily")
		}
		return searchTavilyResourcesForNode(node, sources, limit)
	}
	return searchDuckDuckGoResources(node, sources, limit)
}

func mockArticleResources(node model.RoadmapNode, sources []articleSearchSource, limit int) []model.RoadmapResource {
	if len(sources) == 0 {
		sources = selectedArticleSearchSources(nil)
	}
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
		query := url.Values{"q": []string{node.Title}}.Encode()
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

func searchTavilyResourcesForNode(node model.RoadmapNode, sources []articleSearchSource, limit int) ([]model.RoadmapResource, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"api_key":     strings.TrimSpace(os.Getenv("ARTICLE_SEARCH_API_KEY")),
		"query":       buildArticleSearchQuery(node, sources),
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
	return ensureArticleResourceChoices(node, sources, resources, limit), nil
}

func searchDuckDuckGoResources(node model.RoadmapNode, sources []articleSearchSource, limit int) ([]model.RoadmapResource, error) {
	searchURL := "https://duckduckgo.com/html/?" + url.Values{"q": []string{buildArticleSearchQuery(node, sources)}}.Encode()
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
	return ensureArticleResourceChoices(node, sources, extractDuckDuckGoResults(doc, limit), limit), nil
}

func buildArticleSearchQuery(node model.RoadmapNode, sources []articleSearchSource) string {
	for _, query := range node.ArticleSearchQueries {
		if trimmed := strings.TrimSpace(query); trimmed != "" {
			return strings.Join(nonEmptyStrings([]string{trimmed, articleSearchSourceQuery(sources)}), " ")
		}
	}
	return strings.Join(nonEmptyStrings([]string{
		node.Title,
		node.Deliverable,
		"official documentation tutorial technical article",
		articleSearchSourceQuery(sources),
	}), " ")
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
		}
	}
	if len(selected) == 0 {
		return articleSearchSourceCatalog
	}
	return selected
}

func articleSearchSourceQuery(sources []articleSearchSource) string {
	if len(sources) == 0 {
		sources = selectedArticleSearchSources(nil)
	}
	hints := make([]string, 0, len(sources))
	for _, source := range sources {
		if strings.TrimSpace(source.QueryHint) != "" {
			hints = append(hints, source.QueryHint)
		}
	}
	return strings.Join(hints, " ")
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
		if resource.URL == "" || seen[resource.URL] {
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
	limit = normalizeArticleSearchLimit(limit)
	limited := limitArticleResources(resources, limit)
	if len(limited) >= limit {
		return limited
	}
	if len(sources) == 0 {
		sources = selectedArticleSearchSources(nil)
	}

	seen := map[string]bool{}
	for _, resource := range limited {
		seen[resource.URL] = true
	}
	query := strings.Join(nonEmptyStrings([]string{node.Title, node.Deliverable, "technical article tutorial"}), " ")
	for index := 0; len(limited) < limit; index++ {
		source := sources[index%len(sources)]
		searchURL := sourceSearchURL(source, query, index)
		if seen[searchURL] {
			continue
		}
		seen[searchURL] = true
		limited = append(limited, model.RoadmapResource{
			Title:   fmt.Sprintf("%s %s 搜索入口 %d", node.Title, source.Label, index+1),
			URL:     searchURL,
			Summary: source.Label + " 源内搜索入口，真实搜索结果不足时用于继续筛选技术文章。",
		})
	}
	return limited
}

func sourceSearchURL(source articleSearchSource, query string, index int) string {
	values := url.Values{"q": []string{query}}
	if index > 0 {
		values.Set("page", strconv.Itoa(index+1))
	}
	separator := "?"
	if strings.Contains(source.MockURL, "?") {
		separator = "&"
	}
	return source.MockURL + separator + values.Encode()
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
