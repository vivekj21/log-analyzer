#!/usr/bin/env python3
# Tiny fake model server for tests: speaks the OpenAI chat-completions format and the Anthropic messages format.
# First call asks for a tool; the call after the tool result gives a final answer. Logs what it received.
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
LOG = sys.argv[2]

def texts(m):
    c = m["content"]
    if isinstance(c, str):
        return [c]
    return [b.get("text", "") for b in (c or []) if isinstance(b, dict)]
def last_text(msgs): return " ".join(texts(msgs[-1]))
def all_text(msgs): return " ".join(t for m in msgs for t in texts(m))
class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        with open(LOG, 'a') as f:
            f.write(json.dumps({"path": self.path, "auth": self.headers.get("Authorization"), "xkey": self.headers.get("x-api-key"), "body": body}) + "\n")
        msgs = body["messages"]
        anthropic = self.path.endswith("/messages")
        has_result = any((m["role"] == "tool") or (m["role"] == "user" and isinstance(m["content"], list) and any(b.get("type") == "tool_result" for b in m["content"])) for m in msgs)
        final = "ROOT_CAUSE_CATEGORY: CRASHLOOP_BAD_CONFIG\n\n**Root Cause:** fake model says the config is bad\n\n**Confidence:** HIGH\n\n**Evidence:**\n- dry-run output was seen\n\n**Recommendations:**\n1. Fix the config\n"
        # A symptom with the word "forgetful" makes the fake model leave out the category line the first time and
        # give it only when asked for it (the real failure seen once with Gemini); DNS_FAIL proves the second answer was used.
        if "no valid ROOT_CAUSE_CATEGORY" in last_text(msgs):
            final = "ROOT_CAUSE_CATEGORY: DNS_FAIL"
        elif "forgetful" in all_text(msgs):
            final = "**Root Cause:** fake forgetful model, no category line\n\n**Confidence:** HIGH\n"
        if anthropic:
            out = ({"content": [{"type": "text", "text": final}], "stop_reason": "end_turn", "usage": {"input_tokens": 10, "output_tokens": 5}} if has_result else
                   {"content": [{"type": "tool_use", "id": "tu_1", "name": "list_pods", "input": {"namespace": "demo"}}], "stop_reason": "tool_use", "usage": {"input_tokens": 10, "output_tokens": 5}})
        else:
            out = ({"choices": [{"message": {"role": "assistant", "content": final}, "finish_reason": "stop"}], "usage": {"total_tokens": 15}} if has_result else
                   {"choices": [{"message": {"role": "assistant", "content": None, "tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "list_pods", "arguments": json.dumps({"namespace": "demo"})}}]}, "finish_reason": "tool_calls"}], "usage": {"total_tokens": 15}})
        data = json.dumps(out).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
