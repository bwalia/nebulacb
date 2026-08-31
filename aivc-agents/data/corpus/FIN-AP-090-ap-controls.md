---
doc_id: FIN-AP-090
title: Accounts Payable Controls and Invoice Exception Handling
source: finance/controls/FIN-AP-090
acl: [finance, exec]
effective_date: 2026-03-01
owner: Group Financial Controller
---

# Accounts Payable Controls and Invoice Exception Handling

## 1. Purpose

This document defines the control framework for supplier invoice processing, including the
handling of exceptions raised by automated matching. It is classified Internal and is
restricted to Finance and Executive staff.

## 2. Automated matching and straight-through processing

Invoices that pass the three-way match within tolerance, are from a supplier in good standing,
and are below GBP 10,000 are processed straight through without human review. All other
invoices are routed to the exception queue.

## 3. Exception categories

Exceptions are classified as one of:

- PRICE_VARIANCE: invoice unit price differs from the purchase order beyond tolerance
- QUANTITY_VARIANCE: invoiced quantity differs from the goods receipt note
- NO_PO: no purchase order reference could be matched
- DUPLICATE_SUSPECT: the same supplier, amount and invoice number appear more than once
- BANK_DETAIL_CHANGE: the payment instruction differs from the supplier master record
- SANCTIONS_HIT: the supplier or its ultimate beneficial owner matched a screening list

## 4. Autonomous resolution limits

An automated system may resolve a PRICE_VARIANCE or QUANTITY_VARIANCE exception without human
approval only where the absolute variance is below GBP 500 and below 5 percent of the invoice
value, and the supplier has no exception raised in the preceding 90 days.

NO_PO, DUPLICATE_SUSPECT, BANK_DETAIL_CHANGE and SANCTIONS_HIT exceptions may never be
resolved autonomously. BANK_DETAIL_CHANGE and SANCTIONS_HIT must be escalated to the Group
Financial Controller and, for sanctions matches, to Group Legal within one working day.

## 5. Segregation of duties

The person who approves an exception may not be the person who created the purchase order or
who maintains the supplier master record. Automated agents count as a single actor for this
purpose and must therefore never both amend a supplier record and approve a payment to it.

## 6. Audit evidence

Every exception decision must record the decision, the identity of the decision maker, the
evidence relied on, and a timestamp. Records are retained for seven years. Where a decision
was made or recommended by an automated system, the record must also identify the system
version and retain the inputs it used.
