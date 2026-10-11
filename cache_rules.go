package main

import (
	"os"
	"path/filepath"
	"strings"
)

type cacheRule struct{ software, root, pattern string }

// Only explicit disposable cache paths are admitted. Cookies, profile roots,
// Local Storage, IndexedDB, settings, extensions and downloads are excluded.
func cacheRules() []cacheRule {
	local, roaming := localAppData(), appDataDir()
	var rules []cacheRule
	for _, browser := range []struct{ name, path string }{
		{"Chrome", filepath.Join(local, "Google", "Chrome", "User Data")},
		{"Edge", filepath.Join(local, "Microsoft", "Edge", "User Data")},
		{"Brave", filepath.Join(local, "BraveSoftware", "Brave-Browser", "User Data")},
	} {
		for _, profile := range []string{"Default", "Profile *"} {
			for _, kind := range []string{"Cache", "Code Cache", "GPUCache"} {
				rules = append(rules, cacheRule{browser.name + " · " + kind, browser.path, filepath.Join(profile, kind)})
			}
		}
	}
	rules = append(rules, cacheRule{"Firefox · 磁盘缓存", filepath.Join(local, "Mozilla", "Firefox", "Profiles"), filepath.Join("*", "cache2")})
	for _, app := range []struct{ name, dir string }{
		{"VS Code", "Code"}, {"VS Code Insiders", "Code - Insiders"},
		{"Discord", "discord"}, {"Discord PTB", "discordptb"}, {"Discord Canary", "discordcanary"},
		{"Slack", "Slack"},
	} {
		for _, kind := range []string{"Cache", "Code Cache", "GPUCache"} {
			rules = append(rules, cacheRule{app.name + " · " + kind, filepath.Join(roaming, app.dir), kind})
		}
		if strings.HasPrefix(app.name, "VS Code") {
			rules = append(rules, cacheRule{app.name + " · 编译缓存", filepath.Join(roaming, app.dir), "CachedData"})
		}
	}
	return rules
}

func cacheRuleMatches(rule cacheRule, path string) bool {
	root := resolvedPathOrOriginal(rule.root)
	if !pathWithin(path, root) || samePath(path, root) {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts, patterns := strings.Split(rel, `\`), strings.Split(rule.pattern, `\`)
	if len(parts) != len(patterns) {
		return false
	}
	for i, pattern := range patterns {
		ok, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(parts[i]))
		if err != nil || !ok {
			return false
		}
		if pattern == "Profile *" {
			id := strings.TrimPrefix(strings.ToLower(parts[i]), "profile ")
			if id == "" || strings.Trim(id, "0123456789") != "" {
				return false
			}
		}
	}
	return true
}

func isCacheDeletable(path string) bool {
	if hasReparseAncestor(path) {
		return false
	}
	clean, err := resolvedPath(path)
	if err != nil {
		return false
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || hasReparseAncestor(clean) {
		return false
	}
	for _, rule := range cacheRules() {
		if cacheRuleMatches(rule, clean) {
			return true
		}
	}
	return false
}
