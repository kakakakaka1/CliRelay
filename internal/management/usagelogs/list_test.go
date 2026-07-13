package usagelogs

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sort"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestLooksLikeAuthIndex(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "live file seed", value: "39a7982984e321e5", want: true},
		{name: "orphan id seed", value: "69e8946f1ffc2d23", want: true},
		{name: "uppercase hex", value: "69E8946F1FFC2D23", want: true},
		{name: "email label", value: "asherandersenloqv@outlook.com", want: false},
		{name: "display name", value: "Codex 主渠道", want: false},
		{name: "too short", value: "39a7982984e321e", want: false},
		{name: "too long", value: "39a7982984e321e5a", want: false},
		{name: "non hex", value: "gggggggggggggggg", want: false},
		{name: "empty", value: "", want: false},
		{name: "spaces", value: "  39a7982984e321e5  ", want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := looksLikeAuthIndex(tc.value); got != tc.want {
				t.Fatalf("looksLikeAuthIndex(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestChannelFilterSelectorsTreatsOrphanAuthIndexAsAuthIndex(t *testing.T) {
	t.Parallel()

	// Live auth currently uses the file: seed index; historical rows still use
	// the id: seed index for the same OAuth email label.
	liveIndex := "39a7982984e321e5"
	orphanIndex := "69e8946f1ffc2d23"
	label := "asherandersenloqv@outlook.com"

	authIndexChannelMap := map[string]string{liveIndex: label}
	authMetaByIndex := map[string]authChannelMeta{
		liveIndex: {label: label, provider: "xai", authType: "oauth"},
	}

	// Selecting the orphan facet value must stay on AuthIndexes. The previous
	// bug fell through to ChannelNames and queried channel_name = <hash>.
	authIndexes, channelNames, _ := channelFilterSelectors(
		[]string{orphanIndex},
		nil,
		authIndexChannelMap,
		nil,
		authMetaByIndex,
		nil,
	)
	if !reflect.DeepEqual(authIndexes, []string{orphanIndex}) {
		t.Fatalf("authIndexes = %#v, want [%s]", authIndexes, orphanIndex)
	}
	if len(channelNames) != 0 {
		t.Fatalf("channelNames = %#v, want empty", channelNames)
	}

	// Live index still resolves normally.
	authIndexes, channelNames, _ = channelFilterSelectors(
		[]string{liveIndex},
		nil,
		authIndexChannelMap,
		nil,
		authMetaByIndex,
		nil,
	)
	if !reflect.DeepEqual(authIndexes, []string{liveIndex}) {
		t.Fatalf("live authIndexes = %#v, want [%s]", authIndexes, liveIndex)
	}
	if len(channelNames) != 0 {
		t.Fatalf("live channelNames = %#v, want empty", channelNames)
	}

	// Email/display labels still use the legacy channel_name path (and may also
	// expand to live auth indexes via authIndexChannelMap label matching).
	authIndexes, channelNames, _ = channelFilterSelectors(
		[]string{label},
		map[string]string{label: label},
		authIndexChannelMap,
		nil,
		authMetaByIndex,
		nil,
	)
	if !reflect.DeepEqual(authIndexes, []string{liveIndex}) {
		t.Fatalf("label authIndexes = %#v, want [%s]", authIndexes, liveIndex)
	}
	if !reflect.DeepEqual(channelNames, []string{label}) {
		t.Fatalf("label channelNames = %#v, want [%s]", channelNames, label)
	}
}

func seedIndex(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

func TestXaiOAuthAuthIndexGroupMergesIDAndFileSeeds(t *testing.T) {
	t.Parallel()

	fileName := "xai-asherandersenloqv@outlook.com.json"
	live := seedIndex("file:" + fileName)
	orphan := seedIndex("id:" + fileName)

	auth := &coreauth.Auth{
		Provider: "xai",
		FileName: fileName,
		ID:       fileName,
		Label:    "asherandersenloqv@outlook.com",
		Metadata: map[string]any{"email": "asherandersenloqv@outlook.com"},
	}
	group := xaiOAuthAuthIndexGroup(auth)
	if len(group) < 2 {
		t.Fatalf("group = %#v, want at least live+orphan", group)
	}
	if group[0] != live {
		t.Fatalf("canonical = %s, want live %s", group[0], live)
	}
	foundOrphan := false
	for _, member := range group {
		if member == orphan {
			foundOrphan = true
			break
		}
	}
	if !foundOrphan {
		t.Fatalf("group %#v missing orphan %s", group, orphan)
	}
}

func TestXaiOAuthAuthIndexGroupSkipsNonXAI(t *testing.T) {
	t.Parallel()

	fileName := "codex-yuan364299311@gmail.com-pro.json"
	auth := &coreauth.Auth{
		Provider: "codex",
		FileName: fileName,
		ID:       fileName,
		Label:    "yuan364299311@gmail.com",
		Metadata: map[string]any{"email": "yuan364299311@gmail.com"},
	}
	group := xaiOAuthAuthIndexGroup(auth)
	live := seedIndex("file:" + fileName)
	if !reflect.DeepEqual(group, []string{live}) {
		t.Fatalf("codex group = %#v, want only live [%s]", group, live)
	}
}

func TestChannelFilterSelectorsExpandsXaiOAuthIndexGroup(t *testing.T) {
	t.Parallel()

	liveIndex := "39a7982984e321e5"
	orphanIndex := "69e8946f1ffc2d23"
	label := "asherandersenloqv@outlook.com"
	group := []string{liveIndex, orphanIndex}

	authIndexChannelMap := map[string]string{
		liveIndex:   label,
		orphanIndex: label,
	}
	authMetaByIndex := map[string]authChannelMeta{
		liveIndex:   {label: label, provider: "xai", authType: "oauth"},
		orphanIndex: {label: label, provider: "xai", authType: "oauth"},
	}
	authIndexGroup := map[string][]string{
		liveIndex:   group,
		orphanIndex: group,
	}

	// Selecting the live option expands to both historical indexes.
	authIndexes, channelNames, _ := channelFilterSelectors(
		[]string{liveIndex},
		nil,
		authIndexChannelMap,
		nil,
		authMetaByIndex,
		authIndexGroup,
	)
	sort.Strings(authIndexes)
	want := []string{liveIndex, orphanIndex}
	sort.Strings(want)
	if !reflect.DeepEqual(authIndexes, want) {
		t.Fatalf("live expand authIndexes = %#v, want %#v", authIndexes, want)
	}
	if len(channelNames) != 0 {
		t.Fatalf("channelNames = %#v, want empty", channelNames)
	}

	// Selecting the orphan index also expands (old clients / deep links).
	authIndexes, _, _ = channelFilterSelectors(
		[]string{orphanIndex},
		nil,
		authIndexChannelMap,
		nil,
		authMetaByIndex,
		authIndexGroup,
	)
	sort.Strings(authIndexes)
	if !reflect.DeepEqual(authIndexes, want) {
		t.Fatalf("orphan expand authIndexes = %#v, want %#v", authIndexes, want)
	}
}

func TestEnrichChannelFilterOptionsCollapsesXaiOAuthAliases(t *testing.T) {
	t.Parallel()

	liveIndex := "39a7982984e321e5"
	orphanIndex := "69e8946f1ffc2d23"
	label := "asherandersenloqv@outlook.com"
	group := []string{liveIndex, orphanIndex}

	authIndexChannelMap := map[string]string{
		liveIndex:   label,
		orphanIndex: label,
	}
	authMetaByIndex := map[string]authChannelMeta{
		liveIndex:   {label: label, provider: "xai", authType: "oauth"},
		orphanIndex: {label: label, provider: "xai", authType: "oauth"},
	}
	authIndexGroup := map[string][]string{
		liveIndex:   group,
		orphanIndex: group,
	}

	// SQL facet still returns both historical (channel_name, auth_index) pairs.
	// codex same-email must remain a separate option.
	codexIndex := "a9757e6dce652490"
	options := []usage.ChannelFilterOption{
		{Value: orphanIndex, Label: label, AuthIndex: orphanIndex},
		{Value: liveIndex, Label: label, AuthIndex: liveIndex},
		{
			Value:     codexIndex,
			Label:     "yuan364299311@gmail.com",
			AuthIndex: codexIndex,
			Provider:  "codex",
			AuthType:  "oauth",
		},
	}
	authIndexChannelMap[codexIndex] = "yuan364299311@gmail.com"
	authMetaByIndex[codexIndex] = authChannelMeta{
		label:    "yuan364299311@gmail.com",
		provider: "codex",
		authType: "oauth",
	}

	got := enrichChannelFilterOptions(options, nil, authIndexChannelMap, authMetaByIndex, authIndexGroup)

	var asher *usage.ChannelFilterOption
	var codex *usage.ChannelFilterOption
	for i := range got {
		switch got[i].AuthIndex {
		case liveIndex, orphanIndex:
			if asher != nil {
				t.Fatalf("expected one asher option, got multiple: %#v", got)
			}
			asher = &got[i]
		case codexIndex:
			codex = &got[i]
		}
	}
	if asher == nil {
		t.Fatalf("missing merged asher option: %#v", got)
	}
	if asher.AuthIndex != liveIndex {
		t.Fatalf("asher AuthIndex = %s, want live %s", asher.AuthIndex, liveIndex)
	}
	if asher.Value != liveIndex {
		t.Fatalf("asher Value = %s, want live %s", asher.Value, liveIndex)
	}
	if asher.Provider != "xai" {
		t.Fatalf("asher Provider = %q, want xai", asher.Provider)
	}
	if asher.AuthType != "oauth" {
		t.Fatalf("asher AuthType = %q, want oauth", asher.AuthType)
	}
	if codex == nil {
		t.Fatalf("codex option was dropped: %#v", got)
	}
}
