package article

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidOrigin   = errors.New("文章来源身份无效")
	ErrInvalidRevision = errors.New("文章修订无效")
	ErrInvalidState    = errors.New("文章状态不允许该操作")
	ErrForbidden       = errors.New("无权操作该文章")
	ErrVersionConflict = errors.New("文章版本冲突")
	ErrAdminOffline    = errors.New("文章已被管理员下架")
)

type OriginType string

const (
	OriginRSS  OriginType = "rss"
	OriginUser OriginType = "user"
)

const (
	StatusDraft   Status = "draft"
	StatusOffline Status = "offline"
	StatusDeleted Status = "deleted"
)

type OfflineReason string

const (
	OfflineByAuthor OfflineReason = "author"
	OfflineByAdmin  OfflineReason = "admin"
)

// Origin 使用二选一结构表达 RSS 与用户来源，避免组合出伪造的混合身份。
type Origin struct {
	RSS  *RSSOrigin
	User *UserOrigin
}

type RSSOrigin struct {
	SourceID     int64
	DedupeKey    string
	CanonicalURL string
	SourceItemID *string
}

type UserOrigin struct {
	AuthorUserID string
}

func (o Origin) Type() OriginType {
	if o.RSS != nil && o.User == nil {
		return OriginRSS
	}
	if o.User != nil && o.RSS == nil {
		return OriginUser
	}
	return ""
}

func (o Origin) Validate() error {
	switch o.Type() {
	case OriginRSS:
		if o.RSS.SourceID <= 0 || len(o.RSS.DedupeKey) != 64 || strings.TrimSpace(o.RSS.CanonicalURL) == "" {
			return ErrInvalidOrigin
		}
		if _, err := NormalizeCanonicalURL(o.RSS.CanonicalURL); err != nil {
			return ErrInvalidOrigin
		}
	case OriginUser:
		if strings.TrimSpace(o.User.AuthorUserID) == "" {
			return ErrInvalidOrigin
		}
	default:
		return ErrInvalidOrigin
	}
	return nil
}

// Revision 的字段保持私有；内容变化只能构造下一修订，不能原地修改历史修订。
type Revision struct {
	id          int64
	articleID   int64
	number      int
	title       string
	markdown    *string
	contentHash string
	createdAt   time.Time
}

// RevisionData 是不可变修订写入与作者私有读取所需的领域数据。
type RevisionData struct {
	Title            string
	Markdown         *string
	SanitizedHTML    *string
	PlainText        string
	Excerpt          string
	Language         string
	ContentHash      string
	SanitizerVersion int
}

type StoredArticle struct {
	ID             int64
	Origin         OriginType
	AuthorUserID   *string
	Status         Status
	OfflineReason  *OfflineReason
	PublishedAt    *time.Time
	RevisionID     int64
	RevisionNumber int
	LockVersion    int64
	Revision       RevisionData
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func NewRevision(id, articleID int64, number int, title string, markdown *string, contentHash string, createdAt time.Time) (Revision, error) {
	if articleID <= 0 || number <= 0 || len(contentHash) != 64 || createdAt.IsZero() {
		return Revision{}, ErrInvalidRevision
	}
	return Revision{id: id, articleID: articleID, number: number, title: title, markdown: copyString(markdown), contentHash: contentHash, createdAt: createdAt.UTC()}, nil
}

func (r Revision) ID() int64            { return r.id }
func (r Revision) ArticleID() int64     { return r.articleID }
func (r Revision) Number() int          { return r.number }
func (r Revision) Title() string        { return r.title }
func (r Revision) Markdown() *string    { return copyString(r.markdown) }
func (r Revision) ContentHash() string  { return r.contentHash }
func (r Revision) CreatedAt() time.Time { return r.createdAt }

// Aggregate 保存文章身份、生命周期、当前不可变修订和独立乐观锁版本。
type Aggregate struct {
	id              int64
	origin          Origin
	status          Status
	offlineReason   *OfflineReason
	offlineByUserID *string
	publishedAt     *time.Time
	deletedAt       *time.Time
	currentRevision Revision
	lockVersion     int64
	updatedAt       time.Time
}

func NewUserDraft(id int64, authorUserID string, revision Revision, now time.Time) (Aggregate, error) {
	origin := Origin{User: &UserOrigin{AuthorUserID: authorUserID}}
	if err := validateNewAggregate(id, origin, revision, now); err != nil {
		return Aggregate{}, err
	}
	return Aggregate{id: id, origin: origin, status: StatusDraft, currentRevision: revision, lockVersion: 1, updatedAt: now.UTC()}, nil
}

func NewRSSPublished(id int64, origin RSSOrigin, revision Revision, publishedAt, now time.Time) (Aggregate, error) {
	identity := Origin{RSS: &origin}
	if err := validateNewAggregate(id, identity, revision, now); err != nil || publishedAt.IsZero() {
		return Aggregate{}, ErrInvalidOrigin
	}
	published := publishedAt.UTC()
	return Aggregate{id: id, origin: identity, status: StatusPublished, publishedAt: &published,
		currentRevision: revision, lockVersion: 1, updatedAt: now.UTC()}, nil
}

func validateNewAggregate(id int64, origin Origin, revision Revision, now time.Time) error {
	if id <= 0 || now.IsZero() {
		return ErrInvalidArgument
	}
	if err := origin.Validate(); err != nil {
		return err
	}
	if revision.ArticleID() != id || revision.Number() != 1 {
		return ErrInvalidRevision
	}
	return nil
}

func (a Aggregate) ID() int64                     { return a.id }
func (a Aggregate) Origin() Origin                { return a.origin }
func (a Aggregate) Status() Status                { return a.status }
func (a Aggregate) OfflineReason() *OfflineReason { return copyReason(a.offlineReason) }
func (a Aggregate) PublishedAt() *time.Time       { return copyTime(a.publishedAt) }
func (a Aggregate) DeletedAt() *time.Time         { return copyTime(a.deletedAt) }
func (a Aggregate) CurrentRevision() Revision     { return a.currentRevision }
func (a Aggregate) LockVersion() int64            { return a.lockVersion }
func (a Aggregate) UpdatedAt() time.Time          { return a.updatedAt }

func (a *Aggregate) EditByAuthor(actorUserID string, revision Revision, expectedVersion int64, now time.Time) (bool, error) {
	if err := a.requireAuthor(actorUserID); err != nil {
		return false, err
	}
	if err := a.requireWritable(expectedVersion); err != nil {
		return false, err
	}
	if revision.ArticleID() != a.id || revision.Number() != a.currentRevision.Number()+1 {
		return false, ErrInvalidRevision
	}
	if revision.ContentHash() == a.currentRevision.ContentHash() {
		return false, nil
	}
	a.currentRevision = revision
	a.bump(now)
	return true, nil
}

func (a *Aggregate) UpdateRSS(revision Revision, expectedVersion int64, now time.Time) (bool, error) {
	if a.origin.Type() != OriginRSS {
		return false, ErrForbidden
	}
	if err := a.requireWritable(expectedVersion); err != nil {
		return false, err
	}
	if revision.ArticleID() != a.id || revision.Number() != a.currentRevision.Number()+1 {
		return false, ErrInvalidRevision
	}
	if revision.ContentHash() == a.currentRevision.ContentHash() {
		return false, nil
	}
	a.currentRevision = revision
	a.bump(now)
	return true, nil
}

func (a *Aggregate) PublishByAuthor(actorUserID string, expectedVersion int64, now time.Time) error {
	if err := a.requireAuthor(actorUserID); err != nil {
		return err
	}
	if err := a.requireWritable(expectedVersion); err != nil {
		return err
	}
	if a.status == StatusOffline && a.offlineReason != nil && *a.offlineReason == OfflineByAdmin {
		return ErrAdminOffline
	}
	if a.status != StatusDraft && !(a.status == StatusOffline && a.offlineReason != nil && *a.offlineReason == OfflineByAuthor) {
		return ErrInvalidState
	}
	if a.publishedAt == nil {
		published := now.UTC()
		a.publishedAt = &published
	}
	a.status = StatusPublished
	a.clearOffline()
	a.bump(now)
	return nil
}

func (a *Aggregate) OfflineByAuthor(actorUserID string, expectedVersion int64, now time.Time) error {
	if err := a.requireAuthor(actorUserID); err != nil {
		return err
	}
	if err := a.requireWritable(expectedVersion); err != nil {
		return err
	}
	if a.status != StatusPublished {
		return ErrInvalidState
	}
	reason := OfflineByAuthor
	a.status, a.offlineReason = StatusOffline, &reason
	a.offlineByUserID = nil
	a.bump(now)
	return nil
}

func (a *Aggregate) OfflineByAdministrator(adminUserID string, expectedVersion int64, now time.Time) error {
	if strings.TrimSpace(adminUserID) == "" {
		return ErrForbidden
	}
	if err := a.requireWritable(expectedVersion); err != nil {
		return err
	}
	if a.status != StatusPublished {
		return ErrInvalidState
	}
	reason, actor := OfflineByAdmin, adminUserID
	a.status, a.offlineReason, a.offlineByUserID = StatusOffline, &reason, &actor
	a.bump(now)
	return nil
}

func (a *Aggregate) RestoreByAdministrator(adminUserID string, expectedVersion int64, now time.Time) error {
	if strings.TrimSpace(adminUserID) == "" {
		return ErrForbidden
	}
	if err := a.requireWritable(expectedVersion); err != nil {
		return err
	}
	if a.status != StatusOffline || a.offlineReason == nil || *a.offlineReason != OfflineByAdmin {
		return ErrInvalidState
	}
	a.status = StatusPublished
	a.clearOffline()
	a.bump(now)
	return nil
}

func (a *Aggregate) DeleteByAuthor(actorUserID string, expectedVersion int64, now time.Time) error {
	if err := a.requireAuthor(actorUserID); err != nil {
		return err
	}
	if err := a.requireWritable(expectedVersion); err != nil {
		return err
	}
	a.status = StatusDeleted
	deleted := now.UTC()
	a.deletedAt = &deleted
	a.clearOffline()
	a.bump(now)
	return nil
}

func (a Aggregate) requireAuthor(actorUserID string) error {
	if a.origin.Type() != OriginUser || a.origin.User.AuthorUserID != actorUserID {
		return ErrForbidden
	}
	return nil
}

func (a Aggregate) requireWritable(expectedVersion int64) error {
	if a.lockVersion != expectedVersion {
		return ErrVersionConflict
	}
	if a.status == StatusDeleted {
		return ErrInvalidState
	}
	return nil
}

func (a *Aggregate) bump(now time.Time) {
	a.lockVersion++
	a.updatedAt = now.UTC()
}

func (a *Aggregate) clearOffline() {
	a.offlineReason = nil
	a.offlineByUserID = nil
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func copyReason(value *OfflineReason) *OfflineReason {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
