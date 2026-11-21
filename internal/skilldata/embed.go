package skilldata

import _ "embed"

//go:embed SKILL.md
var SkillMarkdown string

//go:embed check_proxy.sh
var CheckProxyScript string
