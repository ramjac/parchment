```parchment-meta
{
  "parchment_format": "parchment-single-file-v2",
  "kind": "spreadsheet",
  "format_version": 2,
  "created_at": "2026-01-15T09:00:00Z",
  "modified_at": "2026-11-20T00:49:10.36871913Z"
}
```

<!-- parchment-body -->

# Parchment product plan

This workbook tracks effort across Parchment's file-first experience. Use the
`Overview` sheet for rollups and `Work Plan` for the editable inputs. Variance
is planned hours minus actual hours: positive values are under plan, and
negative values are over plan.

<!-- parchment-blocks -->

```parchment-spreadsheet
{
  "version": 1,
  "sheets": [
    {
      "name": "Overview",
      "rows": [
        [
          {
            "value": "Parchment product plan"
          },
          {
            "value": "Hours"
          }
        ],
        [
          {
            "value": "Planned effort"
          },
          {
            "formula": "=SUM('Work Plan'!B2:B6)"
          }
        ],
        [
          {
            "value": "Actual effort"
          },
          {
            "formula": "=SUM('Work Plan'!C2:C6)"
          }
        ],
        [
          {
            "value": "Hours under plan"
          },
          {
            "formula": "=B2-B3"
          }
        ],
        [
          {
            "value": "Average actual per workstream"
          },
          {
            "formula": "=AVERAGE('Work Plan'!C2:C6)"
          }
        ],
        [
          {
            "value": "Largest under-plan workstream"
          },
          {
            "formula": "=MAX('Work Plan'!D2:D6)"
          }
        ],
        [
          {
            "value": "Largest over-plan workstream"
          },
          {
            "formula": "=MIN('Work Plan'!D2:D6)"
          }
        ],
        [
          {
            "value": "Sum"
          },
          {
            "formula": "=SUM(B2:B7)"
          }
        ]
      ]
    },
    {
      "name": "Work Plan",
      "rows": [
        [
          {
            "value": "Workstream"
          },
          {
            "value": "Planned hours"
          },
          {
            "value": "Actual hours"
          },
          {
            "value": "Variance"
          }
        ],
        [
          {
            "value": "Notes and editing"
          },
          {
            "value": "12"
          },
          {
            "value": "10"
          },
          {
            "formula": "=B2-C2"
          }
        ],
        [
          {
            "value": "Documents and PDF"
          },
          {
            "value": "24"
          },
          {
            "value": "21"
          },
          {
            "formula": "=B3-C3"
          }
        ],
        [
          {
            "value": "Spreadsheets"
          },
          {
            "value": "18"
          },
          {
            "value": "20"
          },
          {
            "formula": "=B4-C4"
          }
        ],
        [
          {
            "value": "Presentations and images"
          },
          {
            "value": "14"
          },
          {
            "value": "12"
          },
          {
            "formula": "=B5-C5"
          }
        ],
        [
          {
            "value": "CLI and interoperability"
          },
          {
            "value": "22"
          },
          {
            "value": "17"
          },
          {
            "formula": "=B6-C6"
          }
        ]
      ]
    }
  ]
}
```
