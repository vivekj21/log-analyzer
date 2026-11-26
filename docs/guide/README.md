# The Log Analyzer guide

This guide explains how Log Analyzer works and **why it is built the way it is**. It starts from the whole picture and goes down one layer at a time. It is written for three kinds of reader:

- **You want to use it** and want to know what it does with your cluster and your logs. Read chapter 1, then [Where your data goes](../DATA-FLOW.md).
- **You want to contribute.** Read chapters 1 to 4, then the chapter for the part you will touch.
- **You want to learn how a safe AI agent is built**, or you need to answer hard questions about it. Read everything in order, then use the [decision register](decisions.md).

## How every chapter is built

Every chapter has the same eight parts, so after one chapter you know where to look in the next.

1. **The point in two lines.**
2. **First principles.** The idea from the ground up. Every term is explained the first time it appears (and collected in the [glossary](glossary.md)).
3. **How this repo does it.** Real files and functions, so you can check every claim.
4. **Design decisions.** What was chosen, what else could have been chosen, why not that, what it costs, and what would change the choice.
5. **Across layers.** How this part connects to AI, Kubernetes, security and Go.
6. **Industry standard versus here.** What a mature system does, and the honest gap.
7. **Questions to expect.** Hard questions with answers.
8. **Quiz.** A few questions to check you understood. Answers are folded under each question.

## The chapters

| # | Chapter | Status |
|---|---|---|
| 1 | [The problem and the whole picture](01-the-problem-and-the-whole-picture.md) | available |
| 2 | [AI foundations: how the model is used](02-ai-foundations.md) | available |
| 3 | The agents, the message bus and the state machine | planned |
| 4 | Tools, policy and the executor: how it stays read-only | planned |
| 5 | Redaction and where your data goes | planned |
| 6 | Model providers, cost limits and recordings | planned |
| 7 | Evaluation: the accuracy scorecard and the attack tests | planned |
| 8 | Delivery: CI, releases, signing and the Helm chart | planned |
| 9 | Honest gaps and the roadmap | planned |

Also: the [decision register](decisions.md) (every design decision on one page, with the alternatives) and the [glossary](glossary.md).

## How to read the "why"

Where the repository records the reason for a choice (a code comment, a document, a commit message), the guide says so. Where it does not, the guide says **"the case for"** and gives the reasoning that applies. That is reconstructed reasoning, not a quote from the author. Statements about numbers (accuracy, limits) are true for the version named in the chapter and are checked against the code when the guide is updated.
