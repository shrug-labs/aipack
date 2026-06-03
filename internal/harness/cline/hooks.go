package cline

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/shrug-labs/aipack/internal/domain"
	"github.com/shrug-labs/aipack/internal/harness"
)

type clineHookEventSpec struct {
	portable domain.HookEventName
	native   string
}

var clineHookEvents = []clineHookEventSpec{
	{portable: domain.HookEventRunStart, native: "TaskStart"},
	{portable: domain.HookEventPromptSubmit, native: "UserPromptSubmit"},
	{portable: domain.HookEventToolBefore, native: "PreToolUse"},
	{portable: domain.HookEventToolAfter, native: "PostToolUse"},
	{portable: domain.HookEventCompactBefore, native: "PreCompact"},
}

type clineHookHandler struct {
	Event          string `json:"event"`
	Tool           string `json:"tool,omitempty"`
	Source         string `json:"source,omitempty"`
	Command        string `json:"command"`
	CommandWindows string `json:"commandWindows,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	Label          string `json:"label"`
}

func RenderHookWrappers(hooks []domain.Hook) (map[string][]byte, string, []domain.Warning, error) {
	if len(hooks) == 0 {
		return nil, "", nil, nil
	}
	byEvent := map[string][]clineHookHandler{}
	for _, hook := range hooks {
		for eventIndex, event := range hook.Events {
			spec, err := clineNativeHookEvent(event.On)
			if err != nil {
				return nil, "", nil, fmt.Errorf("hook %s/%s events[%d]: %w", hook.SourcePack, hook.ID, eventIndex, err)
			}
			for handlerIndex, handler := range event.EffectiveHandlers() {
				native, err := clineNativeHookHandler(hook, event, handler, eventIndex, handlerIndex)
				if err != nil {
					return nil, "", nil, err
				}
				byEvent[spec.native] = append(byEvent[spec.native], native)
			}
		}
	}
	if len(byEvent) == 0 {
		return nil, "", nil, nil
	}
	out := map[string][]byte{}
	for _, spec := range clineHookEvents {
		handlers := byEvent[spec.native]
		if len(handlers) == 0 {
			continue
		}
		content, err := renderClineHookWrapper(handlers)
		if err != nil {
			return nil, "", nil, fmt.Errorf("render cline hook wrapper %s: %w", spec.native, err)
		}
		out[clineHookWrapperFile(spec.native)] = content
	}
	return out, harness.CompositeSourcePack(hooks, func(h domain.Hook) string { return h.SourcePack }), nil, nil
}

func clineNativeHookEvent(event domain.HookEventName) (clineHookEventSpec, error) {
	for _, spec := range clineHookEvents {
		if spec.portable == event {
			return spec, nil
		}
	}
	return clineHookEventSpec{}, fmt.Errorf("invalid hook event %q", event)
}

func clineHookTraceRefsByEvent(hooks []domain.Hook) map[string][]domain.TraceRef {
	matched := map[string][]domain.Hook{}
	for _, hook := range hooks {
		for _, event := range hook.Events {
			if len(event.EffectiveHandlers()) == 0 {
				continue
			}
			native, err := clineNativeHookEvent(event.On)
			if err != nil {
				continue
			}
			matched[native.native] = append(matched[native.native], hook)
		}
	}
	refs := make(map[string][]domain.TraceRef, len(matched))
	for event, hooks := range matched {
		refs[event] = domain.TraceRefsForHooks(hooks)
	}
	return refs
}

func clineNativeHookHandler(hook domain.Hook, event domain.HookEvent, handler domain.HookHandler, eventIndex, handlerIndex int) (clineHookHandler, error) {
	normalized, err := harness.NormalizeCommandHookHandler(hook, handler, eventIndex, handlerIndex)
	if err != nil {
		return clineHookHandler{}, err
	}
	return clineHookHandler{
		Event:          string(event.On),
		Tool:           domain.NormalizeHookMatcher(event.Match.Tool),
		Source:         domain.NormalizeHookMatcher(event.Match.Source),
		Command:        normalized.Command,
		CommandWindows: normalized.CommandWindows,
		TimeoutSeconds: normalized.TimeoutSeconds,
		Label:          normalized.Label,
	}, nil
}

func clineHookWrapperFile(nativeEvent string) string {
	if runtime.GOOS == "windows" {
		return nativeEvent + ".ps1"
	}
	return nativeEvent
}

func clineHookWrapperMode() os.FileMode {
	if runtime.GOOS == "windows" {
		return 0
	}
	return 0o755
}

func renderClineHookWrapper(handlers []clineHookHandler) ([]byte, error) {
	handlerJSON, err := json.MarshalIndent(handlers, "", "  ")
	if err != nil {
		return nil, err
	}
	if runtime.GOOS == "windows" {
		return []byte(renderClinePowerShellWrapper(handlerJSON)), nil
	}
	var b strings.Builder
	b.WriteString("#!/usr/bin/env node\n")
	b.WriteString("const { spawn } = require(\"node:child_process\");\n\n")
	b.WriteString("const handlers = ")
	b.Write(handlerJSON)
	b.WriteString(";\n\n")
	b.WriteString(clineNodeWrapperRuntime)
	return []byte(b.String()), nil
}

func renderClinePowerShellWrapper(handlerJSON []byte) string {
	encoded := base64.StdEncoding.EncodeToString(handlerJSON)
	return strings.ReplaceAll(clinePowerShellWrapperRuntime, "__AIPACK_HANDLERS_BASE64__", encoded)
}

const clineNodeWrapperRuntime = `function parsePayload(raw) {
  if (!raw.trim()) return {};
  try {
    return JSON.parse(raw);
  } catch (error) {
    console.error("[aipack hooks] failed to parse Cline hook payload: " + error.message);
    return {};
  }
}

function toolName(payload) {
  return payload?.preToolUse?.toolName ?? payload?.postToolUse?.toolName ?? payload?.toolName ?? payload?.tool?.name ?? payload?.tool ?? "";
}

function sourceName(payload, eventName) {
  const source =
    payload?.source ??
    payload?.hook?.source ??
    payload?.task?.source ??
    payload?.taskStart?.source ??
    payload?.preCompact?.source ??
    payload?.compact?.source ??
    "";
  if (source) return source;
  if (eventName === "run.start") return "startup";
  return "";
}

function matcherMatches(pattern, value, options = {}) {
  if (!pattern || pattern === "*") return true;
  const text = String(value ?? "");
  try {
    return new RegExp(pattern, options.caseInsensitive ? "i" : "").test(text);
  } catch (error) {
    console.error("[aipack hooks] invalid matcher /" + pattern + "/: " + error.message);
    return false;
  }
}

function validHookOutput(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const out = { cancel: false };
  if (value.cancel !== undefined) {
    if (typeof value.cancel !== "boolean") return null;
    out.cancel = value.cancel;
  }
  if (value.contextModification !== undefined) {
    if (typeof value.contextModification !== "string") return null;
    out.contextModification = value.contextModification;
  }
  if (value.errorMessage !== undefined) {
    if (typeof value.errorMessage !== "string") return null;
    out.errorMessage = value.errorMessage;
  }
  return out;
}

function parseHookOutput(raw) {
  const stdout = String(raw ?? "");
  if (!stdout.trim()) return null;
  try {
    return validHookOutput(JSON.parse(stdout));
  } catch {}

  const lines = stdout.split("\n");
  let jsonCandidate = "";
  let braceCount = 0;
  let startCollecting = false;
  for (let i = lines.length - 1; i >= 0; i--) {
    const line = lines[i].trimEnd();
    for (let j = line.length - 1; j >= 0; j--) {
      if (line[j] === "}") {
        braceCount++;
        if (!startCollecting) startCollecting = true;
      } else if (line[j] === "{") {
        braceCount--;
      }
    }
    if (startCollecting) jsonCandidate = line + "\n" + jsonCandidate;
    if (startCollecting && braceCount === 0) break;
  }
  if (!jsonCandidate.trim()) return null;
  try {
    const trimmed = jsonCandidate.trim();
    const firstBrace = trimmed.indexOf("{");
    const cleaned = firstBrace === -1 ? trimmed : trimmed.slice(firstBrace);
    return validHookOutput(JSON.parse(cleaned));
  } catch {
    return null;
  }
}

function mergeHookOutputs(outputs) {
  const result = { cancel: outputs.some((output) => output?.cancel === true) };
  const contextModification = outputs
    .map((output) => output?.contextModification?.trim())
    .filter((text) => text)
    .join("\n\n");
  if (contextModification) result.contextModification = contextModification;
  const errorMessage = outputs
    .map((output) => output?.errorMessage?.trim())
    .filter((text) => text)
    .join("\n");
  if (errorMessage) result.errorMessage = errorMessage;
  return result;
}

async function runCommand(handler, rawPayload) {
  const command = process.platform === "win32" && handler.commandWindows ? handler.commandWindows : handler.command;
  if (!command) return null;
  return await new Promise((resolve) => {
    const child = spawn(command, { shell: true, stdio: ["pipe", "pipe", "pipe"] });
    let done = false;
    let stdout = "";
    let stderr = "";
    const finish = (message, output = null) => {
      if (done) return;
      done = true;
      if (timer) clearTimeout(timer);
      if (message) console.error(message);
      resolve(output);
    };
    const timer = handler.timeoutSeconds > 0 ? setTimeout(() => {
      child.kill();
      finish("[aipack hooks] " + handler.label + " timed out after " + handler.timeoutSeconds + "s");
    }, handler.timeoutSeconds * 1000) : null;
    child.stdout.on("data", (chunk) => {
      stdout += String(chunk);
      if (stdout.length > 65536) stdout = stdout.slice(-65536);
    });
    child.stderr.on("data", (chunk) => {
      stderr += String(chunk);
      if (stderr.length > 4096) stderr = stderr.slice(-4096);
    });
    child.stdin.on("error", () => {});
    child.on("error", (error) => finish("[aipack hooks] " + handler.label + " failed to start: " + error.message));
    child.on("close", (code, signal) => {
      const output = parseHookOutput(stdout);
      if (output) {
        if (code !== 0) {
          const suffix = stderr.trim() ? ": " + stderr.trim() : "";
          console.error("[aipack hooks] " + handler.label + " exited " + (signal || code) + " but returned valid JSON" + suffix);
        }
        return finish(null, output);
      }
      if (code === 0) return finish();
      const suffix = stderr.trim() ? ": " + stderr.trim() : "";
      finish("[aipack hooks] " + handler.label + " exited " + (signal || code) + suffix);
    });
    try {
      child.stdin.end(rawPayload);
    } catch {
      finish("[aipack hooks] " + handler.label + " failed to receive stdin payload");
    }
  });
}

async function main() {
  let rawPayload = "";
  process.stdin.setEncoding("utf8");
  for await (const chunk of process.stdin) rawPayload += chunk;
  const payload = parsePayload(rawPayload);
  const name = toolName(payload);
  const outputs = [];
  for (const handler of handlers) {
    if (!matcherMatches(handler.tool, name, { caseInsensitive: true })) continue;
    if (!matcherMatches(handler.source, sourceName(payload, handler.event))) continue;
    const output = await runCommand(handler, rawPayload);
    if (output) outputs.push(output);
  }
  process.stdout.write(JSON.stringify(mergeHookOutputs(outputs)) + "\n");
}

main().catch((error) => {
  console.error("[aipack hooks] wrapper failed: " + (error?.message ?? String(error)));
  process.stdout.write(JSON.stringify({ cancel: false }) + "\n");
});
`

const clinePowerShellWrapperRuntime = `$ErrorActionPreference = "Continue"
$handlersJson = [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String("__AIPACK_HANDLERS_BASE64__"))
$handlers = $handlersJson | ConvertFrom-Json
$rawPayload = [Console]::In.ReadToEnd()
try {
  $payload = $rawPayload | ConvertFrom-Json -ErrorAction Stop
} catch {
  Write-Error "[aipack hooks] failed to parse Cline hook payload: $($_.Exception.Message)"
  $payload = [pscustomobject]@{}
}

function Get-ToolName($payload) {
  if ($null -ne $payload.preToolUse -and $payload.preToolUse.toolName) { return [string]$payload.preToolUse.toolName }
  if ($null -ne $payload.postToolUse -and $payload.postToolUse.toolName) { return [string]$payload.postToolUse.toolName }
  if ($payload.toolName) { return [string]$payload.toolName }
  if ($null -ne $payload.tool -and $payload.tool.name) { return [string]$payload.tool.name }
  if ($payload.tool) { return [string]$payload.tool }
  return ""
}

function Get-SourceName($payload, $eventName) {
  if ($payload.source) { return [string]$payload.source }
  if ($null -ne $payload.hook -and $payload.hook.source) { return [string]$payload.hook.source }
  if ($null -ne $payload.task -and $payload.task.source) { return [string]$payload.task.source }
  if ($null -ne $payload.taskStart -and $payload.taskStart.source) { return [string]$payload.taskStart.source }
  if ($null -ne $payload.preCompact -and $payload.preCompact.source) { return [string]$payload.preCompact.source }
  if ($null -ne $payload.compact -and $payload.compact.source) { return [string]$payload.compact.source }
  if ($eventName -eq "run.start") { return "startup" }
  return ""
}

function Test-MatcherMatch($pattern, $value, [bool]$caseInsensitive = $false) {
  if ([string]::IsNullOrWhiteSpace($pattern) -or $pattern -eq "*") { return $true }
  $text = [string]$value
  try {
    if ($caseInsensitive) { return ($text -match $pattern) }
    return ($text -cmatch $pattern)
  } catch {
    Write-Error "[aipack hooks] invalid matcher /$pattern/: $($_.Exception.Message)"
    return $false
  }
}

function Test-AipackHookOutput($value) {
  if ($null -eq $value) { return $null }
  $props = @($value.PSObject.Properties.Name)
  $result = [ordered]@{ cancel = $false }
  if ($props -contains "cancel") {
    if ($value.cancel -isnot [bool]) { return $null }
    $result.cancel = [bool]$value.cancel
  }
  if ($props -contains "contextModification" -and $null -ne $value.contextModification) {
    if ($value.contextModification -isnot [string]) { return $null }
    $result.contextModification = [string]$value.contextModification
  }
  if ($props -contains "errorMessage" -and $null -ne $value.errorMessage) {
    if ($value.errorMessage -isnot [string]) { return $null }
    $result.errorMessage = [string]$value.errorMessage
  }
  return [pscustomobject]$result
}

function ConvertFrom-AipackHookJsonCandidate($candidate) {
  try {
    return Test-AipackHookOutput ($candidate | ConvertFrom-Json -ErrorAction Stop)
  } catch {
    return $null
  }
}

function ConvertFrom-AipackHookOutput($text) {
  if ([string]::IsNullOrWhiteSpace($text)) { return $null }
  $direct = ConvertFrom-AipackHookJsonCandidate $text.Trim()
  if ($null -ne $direct) { return $direct }

  $lines = [regex]::Split($text, "\r?\n")
  $candidate = ""
  $braceCount = 0
  $collecting = $false
  for ($i = $lines.Length - 1; $i -ge 0; $i--) {
    $line = $lines[$i].TrimEnd()
    for ($j = $line.Length - 1; $j -ge 0; $j--) {
      if ($line[$j] -eq '}') {
        $braceCount++
        if (-not $collecting) { $collecting = $true }
      } elseif ($line[$j] -eq '{') {
        $braceCount--
      }
    }
    if ($collecting) {
      if ($candidate) { $candidate = $line + [Environment]::NewLine + $candidate }
      else { $candidate = $line }
    }
    if ($collecting -and $braceCount -eq 0) { break }
  }
  if ([string]::IsNullOrWhiteSpace($candidate)) { return $null }
  $trimmed = $candidate.Trim()
  $firstBrace = $trimmed.IndexOf("{")
  if ($firstBrace -ge 0) { $trimmed = $trimmed.Substring($firstBrace) }
  return ConvertFrom-AipackHookJsonCandidate $trimmed
}

function Merge-AipackHookOutputs($outputs) {
  $result = [ordered]@{ cancel = $false }
  $contexts = New-Object System.Collections.Generic.List[string]
  $errors = New-Object System.Collections.Generic.List[string]
  foreach ($output in $outputs) {
    if ($null -eq $output) { continue }
    if ($output.cancel -eq $true) { $result.cancel = $true }
    if ($output.PSObject.Properties.Name -contains "contextModification") {
      $text = ([string]$output.contextModification).Trim()
      if ($text) { $contexts.Add($text) }
    }
    if ($output.PSObject.Properties.Name -contains "errorMessage") {
      $text = ([string]$output.errorMessage).Trim()
      if ($text) { $errors.Add($text) }
    }
  }
  if ($contexts.Count -gt 0) { $result.contextModification = [string]::Join([Environment]::NewLine + [Environment]::NewLine, $contexts) }
  if ($errors.Count -gt 0) { $result.errorMessage = [string]::Join([Environment]::NewLine, $errors) }
  return [pscustomobject]$result
}

function Invoke-AipackHookCommand($handler, $rawPayload) {
  $command = $handler.command
  if ($handler.commandWindows) { $command = $handler.commandWindows }
  if ([string]::IsNullOrWhiteSpace($command)) { return }
  $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
  $process = New-Object System.Diagnostics.Process
  $process.StartInfo.FileName = "powershell.exe"
  $process.StartInfo.Arguments = "-NoProfile -ExecutionPolicy Bypass -EncodedCommand $encoded"
  $process.StartInfo.UseShellExecute = $false
  $process.StartInfo.RedirectStandardInput = $true
  $process.StartInfo.RedirectStandardOutput = $true
  $process.StartInfo.RedirectStandardError = $true
  $stdout = New-Object System.Text.StringBuilder
  $stderr = New-Object System.Text.StringBuilder
  $process.add_OutputDataReceived({ if ($_.Data) { [void]$stdout.AppendLine($_.Data) } })
  $process.add_ErrorDataReceived({ if ($_.Data) { [void]$stderr.AppendLine($_.Data) } })
  try {
    [void]$process.Start()
    $process.BeginOutputReadLine()
    $process.BeginErrorReadLine()
    $process.StandardInput.Write($rawPayload)
    $process.StandardInput.Close()
    $timeout = 0
    if ($handler.timeoutSeconds) { $timeout = [int]$handler.timeoutSeconds * 1000 }
    $finished = if ($timeout -gt 0) { $process.WaitForExit($timeout) } else { $process.WaitForExit(); $true }
    if (-not $finished) {
      try { $process.Kill() } catch {}
      Write-Error "[aipack hooks] $($handler.label) timed out after $($handler.timeoutSeconds)s"
      return $null
    }
    $process.WaitForExit()
    $parsed = ConvertFrom-AipackHookOutput $stdout.ToString()
    if ($null -ne $parsed) {
      if ($process.ExitCode -ne 0) {
        $suffix = $stderr.ToString().Trim()
        if ($suffix) { Write-Error "[aipack hooks] $($handler.label) exited $($process.ExitCode) but returned valid JSON: $suffix" }
        else { Write-Error "[aipack hooks] $($handler.label) exited $($process.ExitCode) but returned valid JSON" }
      }
      return $parsed
    } elseif ($process.ExitCode -ne 0) {
      $suffix = $stderr.ToString().Trim()
      if ($suffix) { Write-Error "[aipack hooks] $($handler.label) exited $($process.ExitCode): $suffix" }
      else { Write-Error "[aipack hooks] $($handler.label) exited $($process.ExitCode)" }
    }
  } catch {
    Write-Error "[aipack hooks] $($handler.label) failed: $($_.Exception.Message)"
  }
  return $null
}

$tool = Get-ToolName $payload
$outputs = New-Object System.Collections.Generic.List[object]
foreach ($handler in $handlers) {
  if (-not (Test-MatcherMatch $handler.tool $tool $true)) { continue }
  if (-not (Test-MatcherMatch $handler.source (Get-SourceName $payload $handler.event))) { continue }
  $output = Invoke-AipackHookCommand $handler $rawPayload
  if ($null -ne $output) { [void]$outputs.Add($output) }
}

[Console]::Out.WriteLine((Merge-AipackHookOutputs $outputs | ConvertTo-Json -Compress))
`
