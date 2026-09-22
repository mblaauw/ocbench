package profile

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mbl/ocbench/internal/canon"
	"mbl/ocbench/internal/opencode"
)

const schemaVersion = 1

// consumedKeys are owned by a dedicated component and therefore removed from
// the config catch-all so a single change never produces duplicate diffs
// (spec 5.1).
var consumedKeys = []string{
	"$schema", "agent", "default_agent", "mcp", "model", "permission",
	"plugin", "plugin_origins", "skills", "small_model", "username",
}

// Fingerprint turns a Sources plus Options into an immutable, content-addressed
// Profile. It reads skill and local-plugin files from disk to compute their
// files/local hashes; everything else is a deterministic function of Sources
// and Options.
func Fingerprint(s *Sources, opts Options) (*Profile, error) {
	if s == nil {
		return nil, fmt.Errorf("fingerprint: nil sources")
	}
	raw, err := decodeConfig(s.ResolvedConfig)
	if err != nil {
		return nil, err
	}
	redacted, err := canon.Redact(raw)
	if err != nil {
		return nil, err
	}
	normalized, err := canon.NormalizePaths(redacted, normPrefixes(s.Home)...)
	if err != nil {
		return nil, err
	}
	cfg := toAnyMap(normalized)
	rawCfg := toAnyMap(redacted)

	// NormalizePaths rewrites values, not object keys. plugin_origins is keyed
	// by plugin spec (a file:// URL), so normalize those keys explicitly to keep
	// home paths out of the canonical structure and captures.
	if origins, ok := cfg["plugin_origins"].(map[string]any); ok {
		fixed := make(map[string]any, len(origins))
		for spec, origin := range origins {
			fixed[normalizePath(spec, s.Home)] = origin
		}
		cfg["plugin_origins"] = fixed
	}

	components := map[string]any{}

	primary, environment := buildPrimary(cfg, opts)
	components["primary"] = primary
	components["environment"] = environment

	agents, permissions, err := buildAgents(cfg, s.Agents, s.Home)
	if err != nil {
		return nil, err
	}
	for name, sub := range agents {
		components["agent/"+name] = sub
	}
	components["permissions"] = permissions

	skillCaptures := make([]any, 0, len(s.Skills))
	for _, sk := range s.Skills {
		sub, capture, err := buildSkill(sk, s.Home)
		if err != nil {
			return nil, err
		}
		components["skill/"+sk.Name] = sub
		skillCaptures = append(skillCaptures, capture)
	}
	sortCaptures(skillCaptures)

	for name, entry := range toAnyMap(cfg["mcp"]) {
		components["mcp/"+name] = buildMCP(toAnyMap(entry))
	}

	// Plugins are read from the pre-normalisation capture so a local file can
	// actually be hashed; the component key uses the normalised spec so a home
	// path never leaks into the canonical structure.
	origins := pluginOrigins(rawCfg["plugin_origins"])
	for _, spec := range toStringSlice(rawCfg["plugin"]) {
		sub, err := buildPlugin(spec, origins[spec], s.Home)
		if err != nil {
			return nil, err
		}
		components["plugin/"+normalizePath(spec, s.Home)] = sub
	}

	for scope, content := range s.Instructions {
		path := s.InstructionPaths[scope]
		if path == "" {
			path = defaultInstructionPath(scope, s.Home, s.Dir)
		}
		components["instructions/"+scope] = map[string]any{
			"path":   normalizePath(path, s.Home),
			"sha256": canon.HashBytes(content),
		}
	}

	components["config"] = configCatchAll(cfg)

	snapshot := map[string]any{
		"schema":           schemaVersion,
		"opencode_version": s.OpenCodeVersion,
		"components":       components,
	}
	hash, err := canon.Hash(snapshot)
	if err != nil {
		return nil, err
	}
	canonical, err := canon.JSON(snapshot)
	if err != nil {
		return nil, err
	}
	comps, err := hashedComponents(components)
	if err != nil {
		return nil, err
	}
	resolvedCapture, err := canon.JSON(configCapture(cfg))
	if err != nil {
		return nil, err
	}
	skillsCapture, err := canon.JSON(skillCaptures)
	if err != nil {
		return nil, err
	}
	agentsCapture, err := buildAgentsCapture(s.Agents, s.Home)
	if err != nil {
		return nil, err
	}
	instructionsCapture, err := buildInstructionsCapture(s.Instructions)
	if err != nil {
		return nil, err
	}

	return &Profile{
		Hash:            hash,
		OpenCodeVersion: s.OpenCodeVersion,
		CanonicalJSON:   canonical,
		Components:      comps,
		Snapshot:        snapshot,
		Captures: Captures{
			ResolvedConfig: resolvedCapture,
			Skills:         skillsCapture,
			Agents:         agentsCapture,
			Instructions:   instructionsCapture,
		},
	}, nil
}

// buildPrimary derives the primary selection and the environment component.
// Run-time overrides replace the effective value and are echoed in
// environment.overrides.
func buildPrimary(cfg map[string]any, opts Options) (map[string]any, map[string]any) {
	defaultAgent := stringValue(cfg["default_agent"])
	if opts.Agent != "" {
		defaultAgent = opts.Agent
	}
	model := stringValue(cfg["model"])
	if opts.Model != "" {
		model = opts.Model
	}
	primary := map[string]any{
		"default_agent": defaultAgent,
		"model":         model,
		"small_model":   stringValue(cfg["small_model"]),
	}
	if opts.Variant != "" {
		primary["variant"] = opts.Variant
	}

	envNames := append([]string(nil), opts.EnvNames...)
	sort.Strings(envNames)
	sandbox := opts.SandboxMode
	if sandbox == "" {
		sandbox = "default"
	}
	environment := map[string]any{
		"sandbox":   sandbox,
		"env_names": toAnyStrings(envNames),
		"auto":      opts.Auto,
		"pure":      opts.Pure,
		"overrides": map[string]any{
			"agent":   nullString(opts.Agent),
			"model":   nullString(opts.Model),
			"variant": nullString(opts.Variant),
		},
	}
	return primary, environment
}

// buildAgents returns the agent/<name> subtrees plus the permissions component.
// The resolved config entry is the base; the matching AgentInfo (from
// `opencode debug agent`) enriches it with mode, native and tools. Raw prompt
// text is replaced by prompt_sha256 and permission moves to its own component.
func buildAgents(cfg map[string]any, infos []opencode.AgentInfo, home string) (map[string]map[string]any, map[string]any, error) {
	byName := make(map[string]opencode.AgentInfo, len(infos))
	for _, a := range infos {
		if a.Name != "" {
			byName[a.Name] = a
		}
	}
	declared := toAnyMap(cfg["agent"])

	out := map[string]map[string]any{}
	byAgentPerm := map[string]any{}
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry := toAnyMap(declared[name])
		sub := map[string]any{}
		var err error
		for k, v := range entry {
			if k == "permission" || k == "prompt" {
				continue
			}
			sub[k] = v
		}

		info, hasInfo := byName[name]
		var agentPrompt string
		if hasInfo {
			if info.Mode != "" {
				sub["mode"] = info.Mode
			}
			sub["native"] = info.Native
			if len(info.Tools) > 0 {
				tv, err := decodeRaw(info.Tools)
				if err != nil {
					return nil, nil, fmt.Errorf("agent %s tools: %w", name, err)
				}
				if tv != nil {
					sub["tools"] = tv
				}
			}
			if info.Model != "" {
				sub["model"] = info.Model
			}
			if info.Variant != "" {
				sub["variant"] = info.Variant
			}
			if info.Temperature != nil {
				sub["temperature"] = *info.Temperature
			}
			if info.Steps != nil {
				sub["steps"] = *info.Steps
			}
			if len(info.Options) > 0 {
				ov, err := decodeRaw(info.Options)
				if err != nil {
					return nil, nil, fmt.Errorf("agent %s options: %w", name, err)
				}
				if ov != nil {
					sub["options"] = ov
				}
			}
			if len(info.Permission) > 0 {
				pv, err := decodeRaw(info.Permission)
				if err != nil {
					return nil, nil, fmt.Errorf("agent %s permission: %w", name, err)
				}
				byAgentPerm[name] = pv
			}
			agentPrompt, err = promptHashFromRaw(info.Prompt)
			if err != nil {
				return nil, nil, fmt.Errorf("agent %s prompt: %w", name, err)
			}
		}

		if agentPrompt == "" {
			agentPrompt, err = promptHash(entry["prompt"])
			if err != nil {
				return nil, nil, fmt.Errorf("agent %s prompt: %w", name, err)
			}
		}
		if agentPrompt != "" {
			sub["prompt_sha256"] = agentPrompt
		}
		if _, ok := byAgentPerm[name]; !ok {
			byAgentPerm[name] = entry["permission"]
		}
		if _, ok := sub["options"]; !ok {
			sub["options"] = map[string]any{}
		}

		clean, err := canonicalize(sub, home)
		if err != nil {
			return nil, nil, err
		}
		out[name] = toAnyMap(clean)
	}

	for name, v := range byAgentPerm {
		clean, err := canonicalizePermission(v, home)
		if err != nil {
			return nil, nil, err
		}
		byAgentPerm[name] = clean
	}

	global, err := canonicalizePermission(cfg["permission"], home)
	if err != nil {
		return nil, nil, err
	}
	if global == nil {
		global = map[string]any{}
	}
	return out, map[string]any{"global": global, "by_agent": byAgentPerm}, nil
}

// buildSkill returns the skill component and its capture entry. The capture
// never contains the full skill content.
func buildSkill(sk opencode.SkillInfo, home string) (map[string]any, map[string]any, error) {
	contentHash := canon.HashBytes([]byte(sk.Content))
	source := ""
	filesHash := ""
	if sk.Location != "" {
		source = normalizePath(filepath.Dir(sk.Location), home)
	}
	if dir := filepath.Dir(sk.Location); sk.Location != "" && dir != "." && dir != "" {
		if h, err := hashDir(filepath.Dir(sk.Location)); err == nil {
			filesHash = h
		}
	}

	sub := map[string]any{
		"description":    sk.Description,
		"content_sha256": contentHash,
		"files_sha256":   filesHash,
		"source":         source,
	}
	capture := map[string]any{
		"name":           sk.Name,
		"description":    sk.Description,
		"location":       source,
		"content":        sk.Content,
		"content_sha256": contentHash,
		"files_sha256":   filesHash,
	}
	if filesHash == "" {
		sub["files_sha256"] = contentHash
		capture["files_sha256"] = contentHash
		sub["source_error"] = true
		capture["source_error"] = true
	}
	return sub, capture, nil
}

// buildMCP canonicalises an MCP entry, replacing the environment value map with
// the sorted list of its key names (spec 5.1, 5.3).
func buildMCP(entry map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range entry {
		if k == "environment" {
			continue
		}
		out[k] = v
	}
	out["environment_keys"] = environmentKeyNames(toAnyMap(entry["environment"]))
	return out
}

// environmentKeyNames returns the sorted key names of an MCP environment map.
// It is the single projection shared by the mcp/<name> component and the
// resolved-config capture so neither retains the environment values.
func environmentKeyNames(env map[string]any) []any {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return toAnyStrings(keys)
}

// configCapture projects the resolved config for the raw resolved-config.json
// capture. It mirrors cfg, but every mcp.<name>.environment value map is
// replaced by its sorted key names: redaction only removes values under
// sensitive key names, so a non-sensitive MCP environment value would
// otherwise survive into the capture. The structure fed to hashing and the
// mcp/<name> component are untouched.
func configCapture(cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	mcp, ok := cfg["mcp"].(map[string]any)
	if !ok {
		return out
	}
	mcpOut := make(map[string]any, len(mcp))
	for name, entry := range mcp {
		e := toAnyMap(entry)
		if _, ok := e["environment"]; !ok {
			mcpOut[name] = entry
			continue
		}
		projected := make(map[string]any, len(e))
		for k, v := range e {
			projected[k] = v
		}
		projected["environment"] = environmentKeyNames(toAnyMap(e["environment"]))
		mcpOut[name] = projected
	}
	out["mcp"] = mcpOut
	return out
}

// buildPlugin hashes a local file:// plugin and records its normalised origin.
func buildPlugin(spec string, origin any, home string) (map[string]any, error) {
	sub := map[string]any{}
	if s, ok := origin.(string); ok && s != "" {
		sub["origin"] = normalizePath(s, home)
	}
	if strings.HasPrefix(spec, "file://") {
		local := strings.TrimPrefix(spec, "file://")
		if h, err := hashPath(local); err == nil {
			sub["local_sha256"] = h
		} else {
			sub["local_error"] = true
		}
	}
	return sub, nil
}

// configCatchAll removes the componentised keys from the resolved config.
func configCatchAll(cfg map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		if isConsumed(k) {
			continue
		}
		out[k] = v
	}
	return out
}

func isConsumed(key string) bool {
	for _, k := range consumedKeys {
		if k == key {
			return true
		}
	}
	return false
}

// hashedComponents computes the per-component hash and canonical JSON, sorted
// by key for stable output.
func hashedComponents(components map[string]any) ([]Component, error) {
	keys := make([]string, 0, len(components))
	for k := range components {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Component, 0, len(keys))
	for _, key := range keys {
		kind, name := splitComponentKey(key)
		h, err := canon.Hash(components[key])
		if err != nil {
			return nil, err
		}
		b, err := canon.JSON(components[key])
		if err != nil {
			return nil, err
		}
		out = append(out, Component{Kind: kind, Name: name, Hash: h, CanonicalJSON: b})
	}
	return out, nil
}

func splitComponentKey(key string) (kind, name string) {
	if i := strings.Index(key, "/"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, key
}

// buildAgentsCapture serialises the per-agent debug output, redacted and
// normalised, sorted by name.
func buildAgentsCapture(agents []opencode.AgentInfo, home string) ([]byte, error) {
	type item struct {
		name string
		v    any
	}
	items := make([]item, 0, len(agents))
	seen := map[string]bool{}
	for _, a := range agents {
		raw := []byte(a.Raw)
		if len(raw) == 0 {
			b, err := json.Marshal(a)
			if err != nil {
				return nil, err
			}
			raw = b
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("agent %s capture: %w", a.Name, err)
		}
		clean, err := canonicalize(v, home)
		if err != nil {
			return nil, err
		}
		// `opencode debug agent` emits permission arrays in nondeterministic
		// order; canonicalise them so agents.json is byte-stable for a given
		// profile hash. Only the permission value is retouched, matching the
		// permissions component.
		if m, ok := clean.(map[string]any); ok {
			if perm, ok := m["permission"]; ok {
				sortedPerm, err := sortPermissionArrays(perm)
				if err != nil {
					return nil, fmt.Errorf("agent %s capture permission: %w", a.Name, err)
				}
				m["permission"] = sortedPerm
			}
		}
		name := a.Name
		if name == "" {
			name = stringValue(toAnyMap(clean)["name"])
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, item{name: name, v: clean})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })
	out := make([]any, len(items))
	for i := range items {
		out[i] = items[i].v
	}
	return canon.JSON(out)
}

// buildInstructionsCapture serialises the raw instruction text keyed by scope
// (canon.JSON sorts the object keys).
func buildInstructionsCapture(contents map[string][]byte) ([]byte, error) {
	out := make(map[string]any, len(contents))
	for scope, content := range contents {
		out[scope] = string(content)
	}
	return canon.JSON(out)
}

// defaultInstructionPath reconstructs the source path of an instruction scope
// when Sources did not carry one (for example a hand-built test Sources). The
// global scope resolves under the config dir; the project scope falls back to
// the run directory.
func defaultInstructionPath(scope, home, dir string) string {
	name := scope
	if i := strings.Index(scope, ":"); i >= 0 {
		name = scope[i+1:]
	}
	switch {
	case strings.HasPrefix(scope, "global:"):
		if home != "" {
			return filepath.Join(home, ".config", "opencode", name)
		}
	case strings.HasPrefix(scope, "project:"):
		if dir != "" {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// canonicalize applies redaction then home path normalisation to a value.
func canonicalize(v any, home string) (any, error) {
	redacted, err := canon.Redact(v)
	if err != nil {
		return nil, err
	}
	return canon.NormalizePaths(redacted, normPrefixes(home)...)
}

// canonicalizePermission applies the standard redaction/normalisation and then
// canonicalises the order of permission arrays. OpenCode returns permission
// arrays in nondeterministic order between identical invocations, so sorting
// the elements by their canonical JSON encoding makes the hashed subtree
// order-independent for any element shape (including the real
// {"permission","action","pattern"} objects) and at any nesting depth. The sort
// is recursive because a permission value may be an object keyed by permission
// name whose rule arrays are nested one level down. Object key order is already
// deterministic through canon.JSON.
func canonicalizePermission(v any, home string) (any, error) {
	clean, err := canonicalize(v, home)
	if err != nil {
		return nil, err
	}
	return sortPermissionArrays(clean)
}

// sortPermissionArrays recursively canonicalises the element order of every
// array inside a permission value. Elements are canonicalised first, then
// sorted by their canonical JSON encoding, so a shuffled nested rule list
// yields the same structure as a sorted one.
func sortPermissionArrays(v any) (any, error) {
	switch t := v.(type) {
	case []any:
		type element struct {
			key string
			val any
		}
		elements := make([]element, 0, len(t))
		for _, e := range t {
			ce, err := sortPermissionArrays(e)
			if err != nil {
				return nil, err
			}
			b, err := canon.JSON(ce)
			if err != nil {
				return nil, err
			}
			elements = append(elements, element{key: string(b), val: ce})
		}
		sort.SliceStable(elements, func(i, j int) bool { return elements[i].key < elements[j].key })
		out := make([]any, len(elements))
		for i := range elements {
			out[i] = elements[i].val
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			ce, err := sortPermissionArrays(e)
			if err != nil {
				return nil, err
			}
			out[k] = ce
		}
		return out, nil
	default:
		return v, nil
	}
}

// normPrefixes are the Plan 1 normalisation prefixes: the home directory, plus
// its file:// URL form so file:// plugin specs normalise too.
func normPrefixes(home string) []canon.PathPrefix {
	if home == "" {
		return nil
	}
	return []canon.PathPrefix{
		{From: home, To: "~"},
		{From: "file://" + home, To: "file://~"},
	}
}

func normalizePath(p, home string) string {
	n, err := canon.NormalizePaths(p, normPrefixes(home)...)
	if err != nil {
		return p
	}
	if s, ok := n.(string); ok {
		return s
	}
	return p
}

// hashDir hashes every regular file under dir as sha256 over sorted
// "relpath\0bytes".
func hashDir(dir string) (string, error) {
	type fileEntry struct {
		rel  string
		data []byte
	}
	var files []fileEntry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, fileEntry{rel: filepath.ToSlash(rel), data: b})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f.rel))
		h.Write([]byte{0})
		h.Write(f.data)
	}
	return canon.SHA256Hex(h.Sum(nil)), nil
}

// hashPath hashes a single file, or the directory tree when path is a
// directory.
func hashPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return hashDir(path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return canon.SHA256Hex(b), nil
}

func decodeConfig(raw []byte) (any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode resolved config: %w", err)
	}
	if v == nil {
		return map[string]any{}, nil
	}
	return v, nil
}

func decodeRaw(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// promptHashFromRaw hashes raw JSON prompt bytes, using the decoded string when
// the prompt is a JSON string.
func promptHashFromRaw(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	v, err := decodeRaw(raw)
	if err != nil {
		return "", err
	}
	return promptHash(v)
}

func promptHash(v any) (string, error) {
	if v == nil {
		return "", nil
	}
	if s, ok := v.(string); ok {
		return canon.HashBytes([]byte(s)), nil
	}
	b, err := canon.JSON(v)
	if err != nil {
		return "", err
	}
	return canon.HashBytes(b), nil
}

func pluginOrigins(v any) map[string]any {
	out := map[string]any{}
	for k, val := range toAnyMap(v) {
		out[k] = val
	}
	return out
}

func sortCaptures(captures []any) {
	sort.SliceStable(captures, func(i, j int) bool {
		return stringValue(toAnyMap(captures[i])["name"]) < stringValue(toAnyMap(captures[j])["name"])
	})
}

func toAnyMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func toStringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toAnyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
