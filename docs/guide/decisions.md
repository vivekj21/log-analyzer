# Decision register

Every design decision in the guide on one page. Each row says what was chosen, what else was possible, and where to read the reasoning. Rows are added as new chapters are published.

| ID | Decision | Main alternatives | Chapter |
|---|---|---|---|
| D1 | Read-only by construction (code builds only read-only commands) | Ask the model nicely; general shell tool with filtering | [1](01-the-problem-and-the-whole-picture.md) |
| D2 | A closed list of 10 typed tools | Let the model write `kubectl` commands | [1](01-the-problem-and-the-whole-picture.md) |
| D3 | Separate agents on a message bus | One function with the same checks | [1](01-the-problem-and-the-whole-picture.md) |
| D4 | A rule-based mode that needs no AI | AI only | [1](01-the-problem-and-the-whole-picture.md) |
| D5 | Go | Python and others | [1](01-the-problem-and-the-whole-picture.md) |
| D6 | Prompting with tools | Fine-tuning; retrieval over runbooks | [2](02-ai-foundations.md) |
| D7 | The model fetches evidence with tools | Paste everything into the prompt | [2](02-ai-foundations.md) |
| D8 | Category as a text line, repaired by a guard | JSON schema / provider structured output | [2](02-ai-foundations.md) |
| D9 | Closed list of 16 categories | Open-ended diagnosis | [2](02-ai-foundations.md) |
| D10 | Random per-investigation delimiter token | Fixed marker lines | [2](02-ai-foundations.md) |
| D11 | One model interface, three live providers plus rule-based | Tie to one provider | [2](02-ai-foundations.md) |
| D12 | Repeated runs and a held-out set | Single run on one set | [2](02-ai-foundations.md) |

Back to the [index](README.md).
