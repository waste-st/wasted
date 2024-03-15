package main

import (
	"path/filepath"
	"strings"
)

var magicStrings [][]string = [][]string{
	{"<!DOCTYPE html", "html"},
	{"<html", "html"},
	{"<head", "html"},
	{"<body", "html"},
	{"#!", "shebang"},
	{"package main\n", "go"},
	{"%YAML 1.", "yaml"},
	{"use v6;", "raku"},
}

var sheBang [][]string = [][]string{
	{"perl", "perl"},
	{"ruby", "ruby"},
	{"python", "python"},
	{"sh", "bash"},
	{"bash", "bash"},
	{"zsh", "bash"},
	{"lua", "lua"},
}

func analyseMagic(in string) (lang string, ok bool) {
	for _, v := range magicStrings {
		if len(in) > len(v[0]) && v[0] == in[:len(v[0])] {
			lang = v[1]
			break
		}
	}

	if x := strings.IndexByte(in, '\n'); lang == "shebang" && x > 0 {
		next := in[x+1:]
		for _, v := range magicStrings {
			if len(next) > len(v[0]) && v[0] == next[:len(v[0])] {
				lang = v[1]
				break
			}
		}
	}

	if lang == "shebang" {
		lang = ""
		path := in[2:]
		for path[0] == ' ' {
			path = path[1:]
		}
		x := strings.IndexAny(path, " \r\n")
		var prog string
		if x > 0 {
			prog = filepath.Base(path[:x])
			if prog == "env" && len(in) > x+1 {
				end := strings.IndexAny(in[x:], " \r\n")
				if end > 2 {
					prog = in[x:end]
				}
			}
			for _, cmd := range sheBang {
				if strings.HasPrefix(prog, cmd[0]) {
					lang = cmd[1]
				}
			}
		}
	}

	if lang == "" || lang == "plain" {
		if strings.Index(in, "\x1B[") != -1 || strings.Index(in, "\x1B]8") != -1 {
			lang = "ansi"
		}
	}

	return lang, len(lang) > 0
}
