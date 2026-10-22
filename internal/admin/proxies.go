package admin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"opencode2api/internal/config"
	"opencode2api/internal/gateway"
	"opencode2api/internal/httpx"
)

func (a *Server) handleProbeProxies(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Indexes []int `json:"indexes"`
	}
	if err := decodeAdminJSON(w, r, &input); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	results := a.manager.ProbeProxies(ctx, 12*time.Second, input.Indexes)
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
