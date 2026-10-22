package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/gateway"
	"opencode2api/internal/httpx"
)

func (a *Server) handleClearConfiguredProxies(w http.ResponseWriter, _ *http.Request) {
	removed := len(a.manager.Config().Proxies)
	_, err := a.manager.Update(func(cfg *config.Config) error {
		removed = len(cfg.Proxies)
		cfg.Proxies = nil
		return nil
	})
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "proxy_clear_failed", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed, "config": a.configView()})
}

func (a *Server) handleImportProxies(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Proxies []string `json:"proxies"`
	}
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(input.Proxies) == 0 {
		writeAdminError(w, http.StatusBadRequest, "empty_import", "no valid proxy entries were provided")
		return
	}
	var added int
	_, err := a.manager.Update(func(cfg *config.Config) error {
		if strings.TrimSpace(cfg.ProxyFile) == "" {
			return errors.New("configure Proxy File (for example proxies.txt) before importing")
		}
		path := cfg.ProxyFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(a.manager.ConfigPath()), path)
		}
		current, err := config.ReadProxyFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read Proxy File: %w", err)
		}
		original, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read Proxy File: %w", err)
		}
		all := append([]string(nil), current...)
		var additions []string
		seen := make(map[string]struct{}, len(current)+len(input.Proxies))
		for _, value := range current {
			clean, pool := config.ParseProxyEntry(value)
			seen[proxyImportKey(clean, pool)] = struct{}{}
		}
		for _, value := range input.Proxies {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if !validImportedProxy(value) {
				return fmt.Errorf("invalid proxy entry: %s", config.RedactURL(value))
			}
			clean, pool := config.ParseProxyEntry(value)
			key := proxyImportKey(clean, pool)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, value)
			additions = append(additions, value)
			added++
		}
		if added == 0 {
			return nil
		}
		if err := replaceProxyFile(path, original, additions); err != nil {
			return fmt.Errorf("write Proxy File: %w", err)
		}
		return nil
	})
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "proxy_import_failed", err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"added": added, "config": a.configView()})
}

func proxyImportKey(value string, pool bool) string {
	return strings.ToLower(value) + fmt.Sprintf("#pool=%t", pool)
}

func replaceProxyFile(path string, original []byte, additions []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".proxies-import-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	content := string(original)
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(additions, "\n") + "\n"
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func validImportedProxy(value string) bool {
	clean, _ := config.ParseProxyEntry(value)
	u, err := url.Parse(clean)
	return err == nil && u.Hostname() != "" && u.Port() != "" && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "socks5" || u.Scheme == "socks5h")
}

func (a *Server) handleProbeProxies(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Indexes []int  `json:"indexes"`
		Channel string `json:"channel"`
	}
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	results := a.manager.ProbeProxies(ctx, 60*time.Second, input.Indexes, input.Channel)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

type deleteProxyInput struct {
	Indexes []int `json:"indexes"`
}

func (a *Server) handleDeleteProxies(w http.ResponseWriter, r *http.Request) {
	var input deleteProxyInput
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(input.Indexes) == 0 {
		writeAdminError(w, http.StatusBadRequest, "empty_selection", "select at least one proxy node")
		return
	}
	cfgSnapshot := a.manager.Config()
	proxies := cfgSnapshot.RuntimeProxies()
	results := make([]gateway.ProxyProbeResult, len(proxies))
	for index, proxy := range proxies {
		results[index] = gateway.ProxyProbeResult{Index: index, Address: config.RedactURL(proxy)}
	}
	if len(results) == 0 {
		writeAdminError(w, http.StatusConflict, "no_proxies", "no proxy nodes are configured")
		return
	}
	selected := make(map[int]struct{}, len(input.Indexes))
	for _, index := range input.Indexes {
		if index < 0 || index >= len(results) {
			writeAdminError(w, http.StatusBadRequest, "invalid_selection", "proxy selection is stale; run the check again")
			return
		}
		selected[index] = struct{}{}
	}
	for _, result := range results {
		if _, ok := selected[result.Index]; ok && strings.TrimSpace(result.Address) == "direct" {
			writeAdminError(w, http.StatusBadRequest, "direct_proxy_selected", "the direct route cannot be deleted from the bulk cleanup")
			return
		}
	}

	var removed int
	var stagedFile, targetFile string
	_, err := a.manager.Update(func(cfg *config.Config) error {
		if len(cfg.RuntimeProxies()) != len(results) {
			return errors.New("proxy list changed during check; refresh the list and try again")
		}
		fileProxies := []string{}
		if cfg.ProxyFile != "" {
			path := cfg.ProxyFile
			if !filepath.IsAbs(path) {
				path = filepath.Join(filepath.Dir(a.manager.ConfigPath()), path)
			}
			loaded, err := config.ReadProxyFile(path)
			if err != nil {
				return err
			}
			fileProxies = loaded
		}
		if len(cfg.Proxies)+len(fileProxies) != len(results) {
			return errors.New("proxy list changed during check; refresh the list and try again")
		}
		for index, proxy := range cfg.Proxies {
			if config.RedactURL(proxy) != results[index].Address {
				return errors.New("proxy list changed during check; refresh the list and try again")
			}
		}
		for index, proxy := range fileProxies {
			if config.RedactURL(proxy) != results[len(cfg.Proxies)+index].Address {
				return errors.New("proxy file changed during check; refresh the list and try again")
			}
		}
		for index := range selected {
			if index >= len(cfg.Proxies) {
				fileIndex := index - len(cfg.Proxies)
				if fileIndex < 0 || fileIndex >= len(fileProxies) {
					return errors.New("proxy file changed during check; refresh the list and try again")
				}
				if config.RedactURL(fileProxies[fileIndex]) != results[index].Address {
					return errors.New("proxy file changed during check; refresh the list and try again")
				}
			} else if config.RedactURL(cfg.Proxies[index]) != results[index].Address {
				return errors.New("proxy list changed during check; refresh the list and try again")
			}
		}
		fileSelected := make(map[int]struct{})
		for index := range selected {
			if index >= len(cfg.Proxies) {
				fileSelected[index-len(cfg.Proxies)] = struct{}{}
			}
		}
		if cfg.ProxyFile != "" && len(fileSelected) > 0 {
			path := cfg.ProxyFile
			if !filepath.IsAbs(path) {
				path = filepath.Join(filepath.Dir(a.manager.ConfigPath()), path)
			}
			var err error
			stagedFile, err = stageProxyFile(path, fileProxies, fileSelected)
			if err != nil {
				return err
			}
			targetFile = path
		}
		kept := make([]string, 0, len(cfg.Proxies))
		for index, value := range cfg.Proxies {
			if _, drop := selected[index]; drop {
				removed++
				continue
			}
			kept = append(kept, value)
		}
		for index := range selected {
			if index >= len(cfg.Proxies) {
				removed++
			}
		}
		cfg.Proxies = kept
		return nil
	})
	if err != nil {
		if stagedFile != "" {
			_ = os.Remove(stagedFile)
		}
		writeAdminError(w, http.StatusBadRequest, "delete_failed", err.Error())
		return
	}
	if stagedFile != "" {
		if err := os.Rename(stagedFile, targetFile); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "proxy_file_commit_failed", "configuration was saved, but the proxy file could not be replaced")
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": removed, "config": a.configView()})
}

func stageProxyFile(path string, proxies []string, selected map[int]struct{}) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	proxyIndex := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(line, "\r"), "\ufeff"))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "//") {
			kept = append(kept, line)
			continue
		}
		if proxyIndex >= len(proxies) {
			return "", errors.New("proxy file changed during check; refresh the list and try again")
		}
		lineProxy := strings.TrimSpace(config.StripProxyLineComment(strings.TrimPrefix(strings.TrimSpace(strings.TrimSuffix(line, "\r")), "\ufeff")))
		if config.RedactURL(lineProxy) != config.RedactURL(proxies[proxyIndex]) {
			return "", errors.New("proxy file changed during check; refresh the list and try again")
		}
		if _, drop := selected[proxyIndex]; drop {
			proxyIndex++
			continue
		}
		kept = append(kept, line)
		proxyIndex++
	}
	if proxyIndex != len(proxies) {
		return "", errors.New("proxy file changed during check; refresh the list and try again")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".proxies-*.tmp")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if _, err := temp.WriteString(strings.Join(kept, "\n")); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return "", err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}
	return tempPath, nil
}
