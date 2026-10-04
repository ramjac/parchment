```parchment-meta
{
  "parchment_format": "parchment-single-file-v1",
  "id": "30000000000000000000000000000003",
  "kind": "spreadsheet",
  "title": "Monthly budget",
  "created_at": "2026-01-15T09:00:00Z",
  "modified_at": "2026-01-15T09:00:00Z",
  "format_version": 1,
  "location": ".parchment/artifacts/30000000000000000000000000000003/content.md"
}
```

```parchment-spreadsheet
{
  "version": 1,
  "sheets": [
    {
      "name": "Sheet1",
      "rows": [
        [
          { "value": "Category" },
          { "value": "Planned" },
          { "value": "Actual" },
          { "value": "Variance" }
        ],
        [
          { "value": "Seeds" },
          { "value": "180" },
          { "value": "162" },
          { "formula": "=B2-C2" }
        ],
        [
          { "value": "Soil" },
          { "value": "240" },
          { "value": "255" },
          { "formula": "=B3-C3" }
        ],
        [
          { "value": "Tools" },
          { "value": "125" },
          { "value": "90" },
          { "formula": "=B4-C4" }
        ],
        [
          { "value": "Water" },
          { "value": "80" },
          { "value": "74" },
          { "formula": "=B5-C5" }
        ]
      ]
    }
  ]
}
```

<!-- parchment-body -->

# Monthly budget

The variance column is calculated from planned and actual spending.
