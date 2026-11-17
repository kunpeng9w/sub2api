// Local Claude Code defaults shared by shell commands and settings.json.
const defaults = [
  'CLAUDE_CODE_ATTRIBUTION_HEADER',
  'DISABLE_TELEMETRY',
  'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC',
  'ENABLE_PROMPT_CACHING_1H'
] as const

export function buildClaudeCodeFiles(shell: string, baseUrl: string, apiKey: string, settingsHint: string) {
  let path = 'Terminal'
  let content = ''
  switch (shell) {
    case 'unix':
      content = `export ANTHROPIC_BASE_URL="${baseUrl}"
export ANTHROPIC_AUTH_TOKEN="${apiKey}"
${defaults.map((name) => `export ${name}=1`).join('\n')}`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set ANTHROPIC_BASE_URL=${baseUrl}
set ANTHROPIC_AUTH_TOKEN=${apiKey}
${defaults.map((name) => `set ${name}=1`).join('\n')}`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:ANTHROPIC_BASE_URL="${baseUrl}"
$env:ANTHROPIC_AUTH_TOKEN="${apiKey}"
${defaults.map((name) => `$env:${name}=1`).join('\n')}`
      break
  }

  return [
    { path, content },
    {
      path: shell === 'unix' ? '~/.claude/settings.json' : '%USERPROFILE%\\.claude\\settings.json',
      content: `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "skipWebFetchPreflight": true,
  "env": {
    "ANTHROPIC_BASE_URL": "${baseUrl}",
    "ANTHROPIC_AUTH_TOKEN": "${apiKey}",
${defaults.map((name) => `    "${name}": "1"`).join(',\n')}
  }
}`,
      hint: settingsHint
    }
  ]
}
