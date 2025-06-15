# Evaluation Summary: Publish/Subscribe System

**Date:** June 17, 2025
**Author:** Ivanusca Teodor, Carp Razvan, Priboiu Mihai

---

## Matching Rate Comparison

The following table provides a comparative overview of the system's performance under two test scenarios, by modifying the selectivity of subscriptions on the `city` field.

| Metric | Scenario 1 (100% Equality) | Scenario 2 (25% Equality) |
| :--- | :--- | :--- |
| **Predominant Operator** | `=` (more selective) | `!=` (less selective) |
| **Total Notifications Delivered** | **329,600** | **581,780** |
| **Aggregated Average Latency** | ~1.411 ms | ~1.441 ms |
| **Match Analysis** | ~18 notifications / publication | ~32 notifications / publication |

---

## Detailed Results per Subscriber

Below is the raw data collected from each subscriber node during the two experiments.

### Scenario 1: 100% Equality Operator Frequency

| Subscriber ID | Simple Notifications | Complex Notifications | Average Latency |
| :--- | :--- | :--- | :--- |
| `91292453-...` | 63,642 | 17,794 | 1.207 ms |
| `88280218-...` | 73,837 | 17,583 | 1.400 ms |
| `f0aca9d8-...` | 119,172 | 37,572 | 1.627 ms |

### Scenario 2: 25% Equality Operator Frequency

| Subscriber ID | Simple Notifications | Complex Notifications | Average Latency |
| :--- | :--- | :--- | :--- |
| `d7a15ed1-...` | 66,932 | 16,630 | 1.178 ms |
| `d64d5074-...` | 61,447 | 15,500 | 1.304 ms |
| `44efe266-...` | 384,418 | 36,853 | 1.840 ms |

---

## Analysis and Conclusions

1.  **Impact of Selectivity:** The results clearly demonstrate how operator selectivity directly influences the system's load. Counter-intuitively, the scenario with only 25% equality operators generated **significantly more** notifications. This is because the `!=` operator (used in 75% of cases in Scenario 2) is far **less selective** than `=`. A subscription for `(city, !=, "Bucharest")` will match publications from all other cities, dramatically increasing the probability of a match.

2.  **Filtering Validation:** This inversion of results perfectly validates the correctness of the filtering engine. The system responded correctly to the change in operator semantics, delivering more messages when the filters became more permissive.

3.  **Consistent Performance:** The average latency remained excellent and almost unchanged between the two tests (~1.4 ms). This indicates that the partition-based routing architecture is robust and is not significantly impacted by the increase in the number of delivered notifications.