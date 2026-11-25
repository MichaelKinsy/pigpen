package github

// Types mirroring omp's tools/gh-types.ts, packages/tui/src/tools/github.ts,
// and packages/tui/src/tools/output-meta.ts. JSON tags match omp's GhToolDetails
// so Tern-native and terminal renderers can draw directly from Details.

// SourceMeta mirrors packages/tui/src/tools/output-meta.ts:51-56.
type SourceMeta struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// OutputMeta mirrors packages/tui/src/tools/output-meta.ts:107-117.
type OutputMeta struct {
	Source *SourceMeta `json:"source,omitempty"`
}

func metaWithSourceURL(u string) *OutputMeta {
	if u == "" {
		return nil
	}
	return &OutputMeta{
		Source: &SourceMeta{
			Type:  "url",
			Value: u,
		},
	}
}

type ghUser struct {
	Login string `json:"login,omitempty"`
	Name  string `json:"name,omitempty"`
}

type ghLabel struct {
	Name string `json:"name,omitempty"`
}

type ghComment struct {
	Author          *ghUser `json:"author,omitempty"`
	Body            string  `json:"body,omitempty"`
	CreatedAt       string  `json:"createdAt,omitempty"`
	URL             string  `json:"url,omitempty"`
	IsMinimized     bool    `json:"isMinimized,omitempty"`
	MinimizedReason string  `json:"minimizedReason,omitempty"`
}

type ghRepoTopic struct {
	Name  string `json:"name,omitempty"`
	Topic *struct {
		Name string `json:"name,omitempty"`
	} `json:"topic,omitempty"`
}

type ghRepoViewData struct {
	NameWithOwner    string                 `json:"nameWithOwner,omitempty"`
	Description      string                 `json:"description,omitempty"`
	URL              string                 `json:"url,omitempty"`
	SSHURL           string                 `json:"sshUrl,omitempty"`
	DefaultBranchRef *struct{ Name string } `json:"defaultBranchRef,omitempty"`
	HomepageURL      string                 `json:"homepageUrl,omitempty"`
	ForkCount        *int                   `json:"forkCount,omitempty"`
	IsArchived       *bool                  `json:"isArchived,omitempty"`
	IsFork           *bool                  `json:"isFork,omitempty"`
	PrimaryLanguage  *struct{ Name string } `json:"primaryLanguage,omitempty"`
	RepositoryTopics []ghRepoTopic          `json:"repositoryTopics,omitempty"`
	StargazerCount   *int                   `json:"stargazerCount,omitempty"`
	UpdatedAt        string                 `json:"updatedAt,omitempty"`
	ViewerPermission string                 `json:"viewerPermission,omitempty"`
	Visibility       string                 `json:"visibility,omitempty"`
}

type ghIssueViewData struct {
	Author      *ghUser     `json:"author,omitempty"`
	Body        string      `json:"body,omitempty"`
	Comments    []ghComment `json:"comments,omitempty"`
	CreatedAt   string      `json:"createdAt,omitempty"`
	Labels      []ghLabel   `json:"labels,omitempty"`
	Number      *int        `json:"number,omitempty"`
	State       string      `json:"state,omitempty"`
	StateReason string      `json:"stateReason,omitempty"`
	Title       string      `json:"title,omitempty"`
	UpdatedAt   string      `json:"updatedAt,omitempty"`
	URL         string      `json:"url,omitempty"`
}

type ghPrFile struct {
	Path       string `json:"path,omitempty"`
	Additions  int    `json:"additions,omitempty"`
	Deletions  int    `json:"deletions,omitempty"`
	ChangeType string `json:"changeType,omitempty"`
}

type ghPrReview struct {
	Author *ghUser `json:"author,omitempty"`
	Body   string  `json:"body,omitempty"`
	Commit *struct {
		OID string `json:"oid,omitempty"`
	} `json:"commit,omitempty"`
	State       string `json:"state,omitempty"`
	SubmittedAt string `json:"submittedAt,omitempty"`
}

type ghPrReviewComment struct {
	Author       *ghUser `json:"author,omitempty"`
	Body         string  `json:"body,omitempty"`
	CreatedAt    string  `json:"createdAt,omitempty"`
	ID           int     `json:"id,omitempty"`
	InReplyToID  *int    `json:"inReplyToId,omitempty"`
	Line         *int    `json:"line,omitempty"`
	OriginalLine *int    `json:"originalLine,omitempty"`
	Path         string  `json:"path,omitempty"`
	Side         string  `json:"side,omitempty"`
	URL          string  `json:"url,omitempty"`
}

type ghPrReviewCommentAPI struct {
	ID           *int    `json:"id,omitempty"`
	User         *ghUser `json:"user,omitempty"`
	Body         string  `json:"body,omitempty"`
	CreatedAt    string  `json:"created_at,omitempty"`
	InReplyToID  *int    `json:"in_reply_to_id,omitempty"`
	Line         *int    `json:"line,omitempty"`
	OriginalLine *int    `json:"original_line,omitempty"`
	Path         string  `json:"path,omitempty"`
	Side         string  `json:"side,omitempty"`
	HTMLURL      string  `json:"html_url,omitempty"`
}

type ghPrViewData struct {
	ghIssueViewData
	BaseRefName         string              `json:"baseRefName,omitempty"`
	Files               []ghPrFile          `json:"files,omitempty"`
	HeadRefName         string              `json:"headRefName,omitempty"`
	HeadRefOID          string              `json:"headRefOid,omitempty"`
	HeadRepository      *ghRepoViewData     `json:"headRepository,omitempty"`
	HeadRepositoryOwner *ghUser             `json:"headRepositoryOwner,omitempty"`
	IsCrossRepository   *bool               `json:"isCrossRepository,omitempty"`
	IsDraft             *bool               `json:"isDraft,omitempty"`
	MaintainerCanModify *bool               `json:"maintainerCanModify,omitempty"`
	MergeStateStatus    string              `json:"mergeStateStatus,omitempty"`
	ReviewComments      []ghPrReviewComment `json:"reviewComments,omitempty"`
	Reviews             []ghPrReview        `json:"reviews,omitempty"`
	ReviewDecision      string              `json:"reviewDecision,omitempty"`
}

// GhToolDetails mirrors packages/tui/src/tools/github.ts:736-752.
// Slice fields without omitempty are encoded as [] when non-nil (for run_watch),
// and pointers/nil slices are omitted for non-watch operations.
type GhToolDetails struct {
	Meta         *OutputMeta            `json:"meta,omitempty"`
	ArtifactID   string                 `json:"artifactId,omitempty"`
	Repo         string                 `json:"repo,omitempty"`
	Branch       string                 `json:"branch,omitempty"`
	WorktreePath string                 `json:"worktreePath,omitempty"`
	Remote       string                 `json:"remote,omitempty"`
	RemoteBranch string                 `json:"remoteBranch,omitempty"`
	HeadSHA      string                 `json:"headSha,omitempty"`
	RunID        int                    `json:"runId,omitempty"`
	RunIDs       *[]int                 `json:"runIds,omitempty"`
	Status       string                 `json:"status,omitempty"`
	Conclusion   string                 `json:"conclusion,omitempty"`
	FailedJobs   *[]string              `json:"failedJobs,omitempty"`
	Watch        *GhRunWatchViewDetails `json:"watch,omitempty"`
	Checkouts    []GhPrCheckoutSummary  `json:"checkouts,omitempty"`
}

// GhPrCheckoutSummary mirrors packages/tui/src/tools/github.ts:755-764.
type GhPrCheckoutSummary struct {
	PrNumber     *int   `json:"prNumber,omitempty"`
	URL          string `json:"url,omitempty"`
	Branch       string `json:"branch"`
	WorktreePath string `json:"worktreePath"`
	Remote       string `json:"remote"`
	RemoteBranch string `json:"remoteBranch"`
	Reused       bool   `json:"reused"`
	ClonedWith   string `json:"clonedWith,omitempty"`
}

// GhRunWatchStepDetails mirrors packages/tui/src/tools/github.ts:767-773.
type GhRunWatchStepDetails struct {
	Number          int    `json:"number"`
	Name            string `json:"name"`
	Status          string `json:"status,omitempty"`
	Conclusion      string `json:"conclusion,omitempty"`
	DurationSeconds *int   `json:"durationSeconds,omitempty"`
}

// GhRunWatchJobDetails mirrors packages/tui/src/tools/github.ts:776-784.
type GhRunWatchJobDetails struct {
	ID              int                      `json:"id"`
	Name            string                   `json:"name"`
	Status          string                   `json:"status,omitempty"`
	Conclusion      string                   `json:"conclusion,omitempty"`
	DurationSeconds *int                     `json:"durationSeconds,omitempty"`
	URL             string                   `json:"url,omitempty"`
	Steps           *[]GhRunWatchStepDetails `json:"steps,omitempty"`
}

// GhRunWatchRunDetails mirrors packages/tui/src/tools/github.ts:787-797.
type GhRunWatchRunDetails struct {
	ID           int                    `json:"id"`
	WorkflowName string                 `json:"workflowName,omitempty"`
	DisplayTitle string                 `json:"displayTitle,omitempty"`
	Status       string                 `json:"status,omitempty"`
	Conclusion   string                 `json:"conclusion,omitempty"`
	Branch       string                 `json:"branch,omitempty"`
	HeadSHA      string                 `json:"headSha,omitempty"`
	URL          string                 `json:"url,omitempty"`
	Jobs         []GhRunWatchJobDetails `json:"jobs"`
}

// GhRunWatchFailedLogDetails mirrors packages/tui/src/tools/github.ts:800-808.
type GhRunWatchFailedLogDetails struct {
	RunID             int    `json:"runId"`
	WorkflowName      string `json:"workflowName,omitempty"`
	JobName           string `json:"jobName"`
	Conclusion        string `json:"conclusion,omitempty"`
	Tail              string `json:"tail,omitempty"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

// GhRunWatchViewDetails mirrors packages/tui/src/tools/github.ts:811-822.
type GhRunWatchViewDetails struct {
	Mode       string                       `json:"mode"`
	State      string                       `json:"state"`
	Repo       string                       `json:"repo"`
	Branch     string                       `json:"branch,omitempty"`
	HeadSHA    string                       `json:"headSha,omitempty"`
	PollCount  *int                         `json:"pollCount,omitempty"`
	Note       string                       `json:"note,omitempty"`
	Run        *GhRunWatchRunDetails        `json:"run,omitempty"`
	Runs       *[]GhRunWatchRunDetails      `json:"runs,omitempty"`
	FailedLogs []GhRunWatchFailedLogDetails `json:"failedLogs"`
}
