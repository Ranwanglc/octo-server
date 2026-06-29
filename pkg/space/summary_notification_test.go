package space

import "testing"

// TestSystemBots_ContainsSummaryNotification 守卫 PR#483 (OCT-5) step2：固定常量
// summary_notification 必须在 SystemBots 中，使 IsSystemBot 返回 true，下游所有
// 过滤（member_count 分析排除、sync 注入、@选择器、search）自动覆盖。
func TestSystemBots_ContainsSummaryNotification(t *testing.T) {
	if !IsSystemBot(SummaryNotificationBotUID) {
		t.Fatalf("IsSystemBot(%q) = false, want true (PR#483 step2)", SummaryNotificationBotUID)
	}
	if SummaryNotificationBotUID != "summary_notification" {
		t.Fatalf("SummaryNotificationBotUID = %q, want %q (拼写: 全小写下划线)", SummaryNotificationBotUID, "summary_notification")
	}
	if !SystemBots[SummaryNotificationBotUID] {
		t.Fatalf("SystemBots[%q] = false, want true", SummaryNotificationBotUID)
	}
	// SystemBotList 必须包含它（稳定顺序遍历的下游依赖它）。
	found := false
	for _, uid := range SystemBotList() {
		if uid == SummaryNotificationBotUID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SystemBotList() missing %q", SummaryNotificationBotUID)
	}
}

// TestSystemBots_NonSummaryNotFlagged sanity：普通 bot UID 不应被误判为系统 bot。
func TestSystemBots_NonSummaryNotFlagged(t *testing.T) {
	if IsSystemBot("u_regular_bot") {
		t.Fatalf("IsSystemBot(\"u_regular_bot\") = true, want false")
	}
}
