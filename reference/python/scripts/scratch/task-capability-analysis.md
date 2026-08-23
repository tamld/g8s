# Task: Analyze Worker Capability Boundaries from Evidence

Read these files in `/Users/tamld/plugins/agy-dispatch/scripts/`:

1. `agy_harness.py` — Read ALL role definitions (ROLES dict) and permission profiles (PERMISSIONS dict). Note each role's `purpose`, `forbidden` list, and allowed permission levels.

2. `agy_dispatch.py` — Read the full dispatch flow. Note what information workers receive (contract prompt), what constraints they operate under.

3. Existing dispatch results — Read these JSON files to analyze what Flash workers produced:
   - `/Users/tamld/plugins/agy-dispatch/scripts/scratch/test-receipt-result.json` — Worker generated 38 pytest tests
   - `/Users/tamld/plugins/agy-dispatch/scripts/scratch/test-safety-result.json` — Worker generated 32 pytest tests

4. Test files the worker created — analyze quality:
   - `test_receipt_delegation.py` — 38 tests, ALL passed first run
   - `test_safety_coordination.py` — 32 tests, ALL passed first run

5. Read the DESIGN doc for context: `/Users/tamld/plugins/agy-dispatch/docs/DESIGN-receipt-delegation.md`

## Your Analysis

Based on the evidence, produce a structured analysis in this format:

### Section 1: Worker Strengths (tasks where Flash excels)
For each strength, cite specific evidence (file:line or result field).
Consider: test generation, code reading, pattern matching, boilerplate, data collection, scanning.

### Section 2: Worker Weaknesses (tasks where Flash fails or produces poor results)
Consider: architectural decisions, multi-file refactoring, context-heavy debugging, ambiguous specs, governance rule creation, production deployment commands.

### Section 3: Task Classification Matrix
Create a table with columns: Task Type | Worker Capable? | Evidence | Recommended Role | Risk Level

Categories to evaluate:
- Test generation from spec
- Code review / verification
- File scanning / inventory
- Documentation generation
- Bug investigation
- Multi-file refactoring
- Architecture decisions
- Security audit
- Infrastructure commands (SSH, config changes)
- Knowledge note creation
- YAML/frontmatter validation
- Report data collection
- Prompt engineering
- Rule/policy authoring

### Section 4: Optimal Dispatch Patterns
What makes a good dispatch spec? What causes worker failure?
Patterns from the successful dispatches vs known failure modes.

### Section 5: Supervisor Contract
What should Brain do before, during, and after dispatch?
What information must Brain provide for worker success?

### Section 6: Worker Self-Awareness Contract  
What should a worker know about itself?
When should a worker STOP and return NEEDS_INFO vs attempt the task?

Output as clean Markdown.
