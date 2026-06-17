package bot_api

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

// TestAgentTurnChannelTypeLabel verifies the channel_type label stays bounded:
// known WuKongIM channel types map to stable low-cardinality strings, and any
// unknown value collapses to "other" (so a fuzzed/garbage channel_type can't
// explode Prometheus cardinality).
func TestAgentTurnChannelTypeLabel(t *testing.T) {
	cases := []struct {
		name        string
		channelType uint8
		want        string
	}{
		{"person", common.ChannelTypePerson.Uint8(), "person"},
		{"group", common.ChannelTypeGroup.Uint8(), "group"},
		{"topic", common.ChannelTypeCommunityTopic.Uint8(), "topic"},
		{"unknown", 250, "other"},
		{"zero", 0, "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, agentTurnChannelTypeLabel(tc.channelType))
		})
	}
}

// TestObserveAgentTurnDelivery_Success records a successful delivery and asserts
// the ok counter advances (and the error counter does not).
func TestObserveAgentTurnDelivery_Success(t *testing.T) {
	ct := common.ChannelTypeGroup.Uint8()
	okBefore := testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("group", agentTurnResultOK))
	errBefore := testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("group", agentTurnResultError))

	observeAgentTurnDelivery(ct, 0.123, nil)

	assert.Equal(t, okBefore+1, testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("group", agentTurnResultOK)))
	assert.Equal(t, errBefore, testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("group", agentTurnResultError)))
}

// TestObserveAgentTurnDelivery_Error records a failed delivery and asserts the
// error counter (the "failed agent-turn deliveries" signal) advances.
func TestObserveAgentTurnDelivery_Error(t *testing.T) {
	ct := common.ChannelTypePerson.Uint8()
	errBefore := testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("person", agentTurnResultError))
	okBefore := testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("person", agentTurnResultOK))

	observeAgentTurnDelivery(ct, 0.05, errors.New("im dispatch failed"))

	assert.Equal(t, errBefore+1, testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("person", agentTurnResultError)))
	assert.Equal(t, okBefore, testutil.ToFloat64(metricAgentTurnDeliveryTotal.WithLabelValues("person", agentTurnResultOK)))
}

// TestAgentTurnDeliveryDuration_HistogramRegistered confirms the latency
// histogram is registered on the default registry and accepts samples.
func TestAgentTurnDeliveryDuration_HistogramRegistered(t *testing.T) {
	observeAgentTurnDelivery(common.ChannelTypeGroup.Uint8(), 0.2, nil)
	assert.Greater(t, testutil.CollectAndCount(metricAgentTurnDeliveryDuration), 0)
}

// TestAgentTurnDeliveryTotal_AllLabelsPrewarmed verifies init() pre-created the
// full channel_type × result series matrix so dashboards distinguish "zero
// deliveries" from "metric absent".
func TestAgentTurnDeliveryTotal_AllLabelsPrewarmed(t *testing.T) {
	for _, ct := range agentTurnChannelTypeLabels() {
		for _, r := range agentTurnResultLabels() {
			_, err := metricAgentTurnDeliveryTotal.GetMetricWithLabelValues(ct, r)
			assert.NoError(t, err, "label (%s,%s) must be valid", ct, r)
		}
	}
}
