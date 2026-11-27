package qdgj

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxKeyResources     = 16
	maxKeyResourceBytes = 16 << 20
	jsIdentifier        = `[a-zA-Z_$][\w$]*`
)

var (
	publicKeyCallPattern  = regexp.MustCompile("(?s)\\bsetPublicKey\\s*\\(\\s*([\"'`])([^\"'`]+)[\"'`]\\s*\\)")
	publicKeyFieldPattern = regexp.MustCompile("(?s)\\b(?:publicKey|PUBLIC_KEY|loginPublicKey)[\"'`]?\\s*[:=]\\s*[\"'`]([^\"'`]+)[\"'`]")
	scriptTagPattern      = regexp.MustCompile(`(?is)<script\b[^>]*>`)
	scriptSourcePattern   = regexp.MustCompile(`(?i)\s+src\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s>]+))`)
	scriptLiteralPattern  = regexp.MustCompile(`["']([^"'\s<>]+\.js(?:\?[^"'\s<>]*)?)["']`)
	loginRoutePattern     = regexp.MustCompile(`(?s)\b(?:url|path)\s*:\s*["']/mobile/login["'][^}]{0,1000}`)
	chunkIDPattern        = regexp.MustCompile(`\.e\(\s*(\d+)\s*\)`)
	chunkTemplatePattern  = regexp.MustCompile(`["']([^"'\s]+)["']\s*\+\s*[a-zA-Z_$][\w$]*\s*\+\s*["']([^"'\s]*\.js)["']`)

	webpackFullHashPattern    = regexp.MustCompile(`(` + jsIdentifier + `)\.h\s*=\s*function\s*\(\s*\)\s*\{\s*return\s*["']([a-zA-Z0-9_-]{1,128})["']\s*;?\s*\}`)
	webpackHashedChunkPattern = regexp.MustCompile(`(` + jsIdentifier + `)\.u\s*=\s*function\s*\(\s*(` + jsIdentifier + `)\s*\)\s*\{\s*return\s*[^{};]*?` +
		`["']([^"'\s]*)["']\s*\+\s*(` + jsIdentifier + `)\s*\+\s*["']\.["']\s*\+\s*(` + jsIdentifier + `)\.h\s*\(\s*\)\s*\+\s*["']\.js["']`)
)

// FetchLoginPublicKey 读取移动登录页面及其同站点脚本，返回 Base64 DER/SPKI RSA 公钥。
// 支持页面内联公钥、静态脚本引用以及带或不带构建哈希的 Webpack 数字分包。
func (c *Client) FetchLoginPublicKey(ctx context.Context) (string, error) {
	page, err := c.request(ctx, "读取登录页面", http.MethodGet, "", nil, "", false, nil)
	if err != nil {
		return "", err
	}
	queue := []keyResource{{url: c.base, body: page}}
	seen := map[string]bool{c.base.String(): true}
	count, total := 0, len(page)
	var lastError error
	for len(queue) > 0 {
		resource := queue[0]
		queue = queue[1:]
		if resource.body == nil {
			if count >= maxKeyResources {
				break
			}
			count++
			resource.body, err = c.requestURL(ctx, "读取登录脚本", http.MethodGet, resource.url, nil, "", false, nil)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				lastError = fmt.Errorf("%s：%w", resource.url.EscapedPath(), err)
				continue
			}
			total += len(resource.body)
			if total > maxKeyResourceBytes {
				return "", fmt.Errorf("%w：页面资源超过 16 MiB", ErrLoginPublicKeyNotFound)
			}
		}
		key, err := extractLoginPublicKey(resource.body)
		if err != nil {
			return "", err
		}
		if key != "" {
			return key, nil
		}
		var discovered []keyResource
		for _, reference := range loginScriptReferences(resource.body) {
			u, ok := c.keyResourceURL(resource.url, reference)
			if !ok || seen[u.String()] {
				continue
			}
			seen[u.String()] = true
			discovered = append(discovered, keyResource{url: u})
		}
		queue = append(discovered, queue...)
	}
	if lastError != nil {
		return "", fmt.Errorf("%w：%w", ErrLoginPublicKeyNotFound, lastError)
	}
	return "", fmt.Errorf("%w（已读取 %d 个脚本）", ErrLoginPublicKeyNotFound, count)
}

type keyResource struct {
	url  *url.URL
	body []byte
}

func extractLoginPublicKey(source []byte) (string, error) {
	var candidates []string
	for _, match := range publicKeyCallPattern.FindAllSubmatch(source, -1) {
		candidates = append(candidates, string(match[2]))
	}
	for _, match := range publicKeyFieldPattern.FindAllSubmatch(source, -1) {
		candidates = append(candidates, string(match[1]))
	}
	var result string
	for _, candidate := range candidates {
		candidate = strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\/`, "/").Replace(candidate)
		key, err := parseLoginPublicKey(candidate)
		if err != nil {
			continue
		}
		der, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return "", err
		}
		encoded := base64.StdEncoding.EncodeToString(der)
		if result != "" && result != encoded {
			return "", fmt.Errorf("%w：登录资源包含多个不同的 RSA 公钥", ErrLoginPublicKeyNotFound)
		}
		result = encoded
	}
	return result, nil
}

func loginChunkReferences(source []byte) []string {
	var ids [][]byte
	for _, route := range loginRoutePattern.FindAll(source, -1) {
		for _, id := range chunkIDPattern.FindAllSubmatch(route, -1) {
			ids = append(ids, id[1])
		}
	}
	if len(ids) == 0 {
		return nil
	}

	// Webpack 的 .u() 可能通过同一运行时的 .h() 拼出 chunk 文件名。
	// 从当前脚本读取构建哈希，不用入口文件名猜测，也不执行 JavaScript。
	hashes := make(map[string]string)
	for _, match := range webpackFullHashPattern.FindAllSubmatch(source, -1) {
		hashes[string(match[1])] = string(match[2])
	}
	var templates [][2]string
	for _, match := range webpackHashedChunkPattern.FindAllSubmatch(source, -1) {
		runtime, parameter, prefix, chunkID, hashRuntime := string(match[1]), string(match[2]), string(match[3]), string(match[4]), string(match[5])
		if hash := hashes[runtime]; hash != "" && parameter == chunkID && runtime == hashRuntime {
			templates = append(templates, [2]string{prefix, "." + hash + ".js"})
		}
	}
	for _, match := range chunkTemplatePattern.FindAllSubmatch(source, -1) {
		templates = append(templates, [2]string{string(match[1]), string(match[2])})
	}
	var result []string
	for _, id := range ids {
		for _, template := range templates {
			result = append(result, template[0]+string(id)+template[1])
		}
	}
	return result
}

func loginScriptReferences(source []byte) []string {
	result := loginChunkReferences(source)
	var scripts []string
	for _, tag := range scriptTagPattern.FindAll(source, -1) {
		if match := scriptSourcePattern.FindSubmatch(tag); match != nil {
			for _, value := range match[1:] {
				if len(value) > 0 {
					scripts = append(scripts, html.UnescapeString(string(value)))
					break
				}
			}
		}
	}
	for _, match := range scriptLiteralPattern.FindAllSubmatch(source, -1) {
		scripts = append(scripts, html.UnescapeString(string(match[1])))
	}
	var vendors []string
	for _, script := range scripts {
		if strings.Contains(script, "vendor") || strings.Contains(script, "vender") {
			vendors = append(vendors, script)
		} else {
			result = append(result, script)
		}
	}
	return append(result, vendors...)
}

func (c *Client) keyResourceURL(parent *url.URL, reference string) (*url.URL, bool) {
	rel, err := url.Parse(reference)
	if err != nil {
		return nil, false
	}
	u := parent.ResolveReference(rel)
	u.Fragment = ""
	if u.User != nil || u.Scheme != c.base.Scheme || u.Host != c.base.Host || !strings.HasPrefix(u.Path, c.base.Path) || !strings.HasSuffix(strings.ToLower(u.Path), ".js") {
		return nil, false
	}
	return u, true
}
