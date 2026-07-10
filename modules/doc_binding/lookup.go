package doc_binding

// LookupSlug 给其他模块（doc_event_receiver 等）用的最小 slug→binding 反查。
// 未命中返回 (nil, nil) —— 与 store.queryBySlug 一致，交调用端决定 404/忽略。
// Binding 是复制体：避免把 DB row (含 dbr 语义位/时间戳) 漏出去，日后加字段不锁跨包依赖。

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
)

// Binding 是跨包消费者能安全依赖的 slug→挂载点视图；只留投递需要的最小集。
type Binding struct {
	Slug      string
	MountType string // group / thread / space
	GroupNo   string
	ThreadId  string
	SpaceId   string
}

// LookupSlug 反查 slug 对应的 binding。未命中 → (nil, nil)。
func LookupSlug(ctx *config.Context, slug string) (*Binding, error) {
	m, err := newDB(ctx).queryBySlug(slug)
	if err != nil || m == nil {
		return nil, err
	}
	return &Binding{
		Slug:      m.Slug,
		MountType: m.MountType,
		GroupNo:   m.GroupNo,
		ThreadId:  m.ThreadId,
		SpaceId:   m.SpaceId,
	}, nil
}
