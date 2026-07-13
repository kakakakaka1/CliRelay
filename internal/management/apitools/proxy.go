package apitools

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
)

func (s *Service) AuthByIndex(authIndex string) *coreauth.Auth {
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" || s == nil || s.authManager == nil {
		return nil
	}
	auths := s.authManager.ListForTenant(s.tenantID)
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		auth.EnsureIndex()
		if auth.Index == authIndex {
			return auth
		}
	}
	return nil
}

func (s *Service) APICallTransport(auth *coreauth.Auth) http.RoundTripper {
	var proxyCandidates []string
	if s != nil && s.cfg != nil {
		proxyID := ""
		fallbackURL := ""
		if auth != nil {
			proxyID = auth.ProxyID
			fallbackURL = auth.ProxyURL
		}
		if proxyStr := strings.TrimSpace(s.cfg.ResolveProxyURL(proxyID, fallbackURL)); proxyStr != "" {
			proxyCandidates = append(proxyCandidates, proxyStr)
		}
	} else if auth != nil {
		if proxyStr := strings.TrimSpace(auth.ProxyURL); proxyStr != "" {
			proxyCandidates = append(proxyCandidates, proxyStr)
		}
	}

	var sdkCfg *config.SDKConfig
	if s != nil && s.cfg != nil {
		sdkCfg = &s.cfg.SDKConfig
	}
	for _, proxyStr := range proxyCandidates {
		if transport := buildProxyTransport(proxyStr, sdkCfg); transport != nil {
			return transport
		}
	}
	return nil
}

func buildProxyTransport(proxyStr string, sdkCfg *config.SDKConfig) *http.Transport {
	proxyStr = strings.TrimSpace(proxyStr)
	if proxyStr == "" {
		return nil
	}

	proxyURL, errParse := url.Parse(proxyStr)
	if errParse != nil {
		log.WithError(errParse).Debug("parse proxy URL failed")
		return nil
	}
	if proxyURL.Scheme == "" || proxyURL.Host == "" {
		log.Debug("proxy URL missing scheme/host")
		return nil
	}

	if proxyURL.Scheme == "socks5" {
		var proxyAuth *proxy.Auth
		if proxyURL.User != nil {
			username := proxyURL.User.Username()
			password, _ := proxyURL.User.Password()
			proxyAuth = &proxy.Auth{User: username, Password: password}
		}
		dialer, errSOCKS5 := proxy.SOCKS5("tcp", proxyURL.Host, proxyAuth, proxy.Direct)
		if errSOCKS5 != nil {
			log.WithError(errSOCKS5).Debug("create SOCKS5 dialer failed")
			return nil
		}
		return &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
	}

	if proxyURL.Scheme == "http" || proxyURL.Scheme == "https" {
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		util.ApplyTLSConfig(transport, sdkCfg)
		return transport
	}

	log.Debugf("unsupported proxy scheme: %s", proxyURL.Scheme)
	return nil
}
