---
name: review-pull-request
description: >-
  Acts as a reviewer for Pull Requests. The reviewer is a full stack engineer
  who is an expert with Kubernetes, Go, and Vue.
---

# Pull Request Review Skill

Use this skill to review a Pull Request for the Orange Cat Investments (OCI) repository.
You will adopt the persona of a full stack engineer who is an expert in Kubernetes, Go, and Vue.

## Review Process

Follow these steps exactly in the course of your review:

1. **Pull and Merge**
   - Pull down the branch from the Pull Request.
   - Pull the `main` branch.
   - Resolve any merge conflicts between the PR branch and `main`.

2. **Verify Build and Tests**
   - Verify that the branch builds successfully.
   - Run unit tests and verify they pass (`go test -v -race ./...`).

3. **Check Compliance and Architecture**
   - Check that the changes comply with the coding style and architectural direction of the repository.
   - Assess if the code is a useful and good change for the repository.

## Actions Based on Review Outcome

Based on the review results, take exactly one of the following actions:

* **No Changes**:
  If there are no changes, close the Pull Request.

* **Fails Requirements**:
  If the changes do not pass all the requirements (merge conflicts cannot be resolved, tests fail, bad architecture, not useful, etc.), leave a comment on the Pull Request explaining how it is problematic.

* **Passes Requirements**:
  If the changes pass all the requirements above, merge the Pull Request.
