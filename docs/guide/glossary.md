# Glossary

Terms are listed in the order they first appear in the guide. Each one points to the chapter that explains it.

| Term | Meaning | Chapter |
|---|---|---|
| Kubernetes | Software that runs and restarts programs (in containers) across many machines. | 1 |
| Container | A program packaged with everything it needs to run. | 1 |
| Pod | The smallest unit Kubernetes runs: one or more containers that live and die together. | 1 |
| Namespace | A named area inside a Kubernetes cluster, used to keep teams or apps apart. | 1 |
| `kubectl` | The command-line tool for asking a Kubernetes cluster questions or giving it orders. | 1 |
| Event | A short message Kubernetes records about something that happened to an object (for example "image pull failed"). | 1 |
| Symptom | What a person notices ("the site returns errors"). Not the same as the cause. | 1 |
| Root cause | The first thing that went wrong and explains the symptom. | 1 |
| On-call | The person who is responsible for answering an alert at any hour. | 1 |
| P0 to P3 | Urgency levels. P0 is the most urgent, P3 the least. | 1 |
| Incident report | A written account of what broke, why, and what to do. | 1 |
| Agent | A program that decides what to do next and acts, instead of only answering once. | 1 |
| Message bus | The channels agents use to send each other typed messages. | 1 |
| State machine | A fixed set of phases and the allowed moves between them. | 1 |
| Allowlist | A short list of things that are permitted; everything else is refused. | 1 |
| Read-only | Able to look but not change. | 1 |
| LLM (large language model) | A program trained on text that continues text it is given. | 2 |
| Token | A piece of text (a word or part of a word) that the model reads and writes. Services charge and limit by tokens. | 2 |
| Context window | The most text a model can look at in one call. | 2 |
| Prompt | The text sent to the model. | 2 |
| System prompt | Standing instructions sent with every call: the role, the rules, the answer format. | 2 |
| Tool calling (function calling) | The model answers with a request to run a named tool with given arguments, instead of plain text. Your program runs it and sends the result back. | 2 |
| Agent loop | Ask the model, run the tools it asks for, send the results back, repeat until it answers. | 2 |
| Prompt injection | Text in the data that tries to give the model orders. | 2 |
| Delimiter | Marker lines that show where untrusted data starts and ends. | 2 |
| Abstain | Say "I cannot tell" instead of guessing. Here that is `CANNOT_DETERMINE`. | 2 |
| Non-deterministic | The same input can give a different output each time. | 2 |
| Circuit breaker | A switch that stops calls for a while after repeated failures. | 2 |
| Dev set / held-out set | Test cases used while tuning, and test cases kept back to check the result honestly. | 2 |
