"""Scripted user replies to the ddtest questions only the user can answer.

ddtest marks such a question with next_action "ask_user" and lists what it
needs in required_input (ci_runtime:<job>, ci_confirm:<fact>[=<values>], or
the candidate providers for ci_provider_required). When the tested agent ends
its turn at such a question, the runner replies in the same conversation, as
a user would, with only the scripted facts that question asks for. Anything
the script does not cover stops the trial, as without a script.
"""

import json
import re

KINDS = ("ci_runtime", "ci_confirm", "ci_provider")
_NAME = r"[a-z0-9][a-z0-9_-]*"
_VERSION = r"[0-9A-Za-z][0-9A-Za-z.+_-]*"


def parse_answer(text):
    """KIND=VALUE, for example ci_runtime=node=22, ci_runtime=<job>:ruby=3.4,
    ci_confirm=jenkins-plugin, ci_confirm=jenkins-plugin-mode=agentless:datadoghq.com
    or ci_provider=jenkins."""
    kind, separator, value = text.strip().partition("=")
    if not separator or kind not in KINDS or not value or "\n" in value:
        raise ValueError(f"User answer {text!r} must be KIND=VALUE with KIND one of {', '.join(KINDS)}")
    answer = {"kind": kind, "text": text.strip()}
    if kind == "ci_runtime":
        selector, separator, version = value.rpartition("=")
        job, _, runtime = selector.rpartition(":")
        if not separator or not re.fullmatch(_NAME, runtime) or not re.fullmatch(_VERSION, version):
            raise ValueError(f"User answer {text!r} must be ci_runtime=<runtime>=<version> or ci_runtime=<workflow / job>:<runtime>=<version>")
        answer.update(job=job.strip() or None, runtime=runtime, version=version)
    elif kind == "ci_confirm":
        fact, valued, stated = value.partition("=")
        if not re.fullmatch(_NAME, fact) or valued and not stated.strip():
            raise ValueError(f"User answer {text!r} must be ci_confirm=<fact> or ci_confirm=<fact>=<value>")
        answer.update(fact=fact, value=stated.strip() or None)
    else:
        if not re.fullmatch(_NAME, value):
            raise ValueError(f"User answer {text!r} must be ci_provider=<provider>")
        answer.update(provider=value)
    return answer


def load_answers(values, path=None):
    """Answers from repeated --user-answer values and an optional file with one
    answer per line (blank lines and # comments ignored)."""
    texts = list(values or [])
    if path is not None:
        for line in path.read_text().splitlines():
            line = line.strip()
            if line and not line.startswith("#"):
                texts.append(line)
    return [parse_answer(text) for text in texts]


def agent_result(text):
    """The last ddtest agent result (one JSON document with schema_version,
    status and next_action) in a tool output, or None."""
    if not isinstance(text, str):
        return None
    decoder = json.JSONDecoder()
    found = None
    for match in re.finditer(r"(?m)^[ \t]*\{", text):
        try:
            value, _ = decoder.raw_decode(text, match.end() - 1)
        except ValueError:
            continue
        if isinstance(value, dict) and all(key in value for key in ("schema_version", "status", "next_action")):
            found = value
    return found


def tool_output_text(content):
    """Claude tool_result content is a string or a list of text blocks."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(block.get("text", "") for block in content if isinstance(block, dict))
    return ""


def question(result):
    """The user question a ddtest result asks, or None."""
    if not isinstance(result, dict) or result.get("next_action") != "ask_user":
        return None
    required = result.get("required_input")
    instruction = result.get("agent_instruction") if isinstance(result.get("agent_instruction"), str) else ""
    reason = result.get("reason") if isinstance(result.get("reason"), str) else ""
    asked = {"code": result.get("code"), "status": result.get("status"),
             "required_input": [item for item in required if isinstance(item, str)] if isinstance(required, list) else [],
             "location": result.get("location"), "reason": reason}
    if asked["code"] == "ci_runtime_required":
        flag = re.search(r"--ci-runtime ([a-z][\w-]*)=<version>", instruction)
        name = re.search(r"\bwhich (\S+) version\b", reason)
        asked["runtime"] = flag[1] if flag else None
        asked["runtime_name"] = name[1] if name else asked["runtime"]
    return asked


def reply(asked, answers):
    """Compose the user's reply from the answers that cover the question.

    Returns {"reply", "answers", "missing"}; reply is None unless every
    required input is covered. The reply states facts only, in plain words:
    no ddtest flags, commands or guidance beyond the answer itself.
    """
    lines, used, missing = [], [], []
    code, required = asked.get("code"), asked.get("required_input") or []

    def answered(item, answer, line):
        lines.append(line)
        used.append({"required_input": item, "answer": answer["text"]})

    if code == "ci_provider_required":
        chosen = next((answer for answer in answers if answer["kind"] == "ci_provider" and answer["provider"] in required), None)
        if chosen:
            answered("ci_provider", chosen, f"CI provider: {chosen['provider']}.")
        else:
            missing.append("ci_provider:" + "|".join(required))
    elif code in ("ci_runtime_required", "ci_setup_confirmation_required") and required:
        for item in required:
            kind, _, subject = item.partition(":")
            if code == "ci_runtime_required" and kind == "ci_runtime":
                candidates = [answer for answer in answers if answer["kind"] == "ci_runtime" and answer["runtime"] == asked.get("runtime")]
                chosen = (next((answer for answer in candidates if answer["job"] == subject), None)
                          or next((answer for answer in candidates if answer["job"] is None), None))
                if chosen:
                    answered(item, chosen, f"{subject} runs {asked.get('runtime_name') or chosen['runtime']} {chosen['version']}.")
                    continue
            elif code == "ci_setup_confirmation_required" and kind == "ci_confirm":
                fact, valued, _ = subject.partition("=")
                chosen = next((answer for answer in answers if answer["kind"] == "ci_confirm" and answer["fact"] == fact), None)
                # A fact with values needs one; a yes/no fact takes yes (implied) or no.
                if chosen and (bool(chosen["value"]) if valued else chosen["value"] in (None, "yes", "no")):
                    answered(item, chosen, f"{fact}: {chosen['value'] or 'yes'}.")
                    continue
            missing.append(item)
    else:
        missing.append(str(code) if not required else f"{code}: " + "; ".join(required))
    return {"reply": "\n".join(lines) if lines and not missing else None, "answers": used, "missing": missing}
