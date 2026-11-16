//go:build !windows

package main

import "time"

func webView2Version() string { return "" }
func ensureWebView2() error   { return nil }
func fatalDialog(_, _ string) {}
func processStart() time.Time { return time.Now() }
