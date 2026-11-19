package main

import (
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// zhCN translates the few strings the Go side shows: the tray menu,
// notifications, and dialog titles. The frontend has its own catalogue.
var zhCN = map[string]string{
	"Show NimGet":       "显示 NimGet",
	"Pause all":         "全部暂停",
	"Resume all":        "全部继续",
	"Quit NimGet":       "退出 NimGet",
	"Download complete": "下载完成",
	"Enter a magnet link with a complete BitTorrent v1 or v2 info hash.":  "请输入包含完整 BitTorrent v1 或 v2 信息哈希的磁力链接。",
	"Use a magnet link with valid HTTP, HTTPS, or UDP tracker addresses.": "请使用包含有效 HTTP、HTTPS 或 UDP Tracker 地址的磁力链接。",
	"Choose a save folder NimGet can use for this magnet download.":       "请为此磁力下载选择 NimGet 可使用的保存文件夹。",
	"Could not queue the magnet download. Try again.":                     "无法将磁力下载加入队列，请重试。",
	"Download failed":                       "下载失败",
	"Choose a download folder":              "选择下载文件夹",
	"The downloaded file no longer exists.": "下载的文件已不存在。",
	"The download folder no longer exists.": "下载文件夹已不存在。",
}

// normalizeLocale maps a BCP 47 tag to a shipped language.
func normalizeLocale(tag string) string {
	if strings.HasPrefix(strings.ToLower(tag), "zh") {
		return "zh-CN"
	}
	return "en"
}

func (d *Desktop) tr(s string) string {
	if lang, _ := d.locale.Load().(string); lang == "zh-CN" {
		if t, ok := zhCN[s]; ok {
			return t
		}
	}
	return s
}

// trayItem is a tray menu entry relabelled when the language changes.
type trayItem struct {
	item  *application.MenuItem
	label string
}

// SetLocale tells the backend the language the frontend resolved, so the tray
// menu and notifications match it.
func (d *Desktop) SetLocale(tag string) {
	d.locale.Store(normalizeLocale(tag))
	for _, t := range d.tray {
		t.item.SetLabel(d.tr(t.label))
	}
}
