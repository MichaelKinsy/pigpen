package warden

import "regexp"

func regexpCompile(p string) (*regexp.Regexp, error) { return regexp.Compile(p) }
