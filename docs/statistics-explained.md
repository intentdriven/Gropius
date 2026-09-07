# Understanding the historical views

The **Statistics** tab has two halves. The top half is live: the last thousand
requests and the totals for the last hour and the last day, held in memory,
emptied by a restart. The bottom half is historical: four tables over a range
you choose, read from the records kept on this Mac.

This page explains what each of those four tables means, and — the half a
table cannot say for itself — what none of them can show.

To switch recording on, see
[Record request statistics on this Mac](request-statistics.md). For the files
themselves, field by field, see
[Reference: the request statistics store](statistics-store-reference.md).

## Where the figures come from

Every figure is a sum, a count or a percentile over the records already on this
Mac. Gropius reads them, adds them up and hands the panel the totals; the
browser never receives a request's own row. The reading happens on the control
plane, which answers this Mac and nothing else, so the figures go no further
than the panel you are looking at.

They are only as good as what was recorded. The token counts are the model
server's own — Gropius repeats them and does not count tokens itself — and the
timings are Gropius's, measured at the point where it hands the answer back.
Neither is an independent measurement of the other.

## The range

The selector offers seven, thirty and ninety days, and everything the store
still keeps. Days and hours are this Mac's own, not UTC: "most evictions fall
between six and seven in the evening" is a claim about the hours you keep, and
a day boundary drawn in UTC would put a late-evening request on tomorrow.

The line above the tables says which range the figures cover and what bounded
them. One view covers at most 366 days, and one reading covers at most
1,000,000 records — a store at its default size limit holds rather fewer than
that, so the record bound only bites on a store whose limit has been raised. If
either bound applies, the line says so, because a table that quietly stopped
short reads exactly like a quiet month.

## Tokens per day

A row for each model on each day it served anything: the requests it answered,
the tokens in, the tokens out, and their total. Beside them is that model's
share of every token in the range — so the question "which model actually does
the work" is answered by one column, and the answer is often not the model you
would have named.

The share is over the range, not over the day, which is why the same figure
repeats down a model's rows. A day where you served nothing looks the same as a
day where recording was off; the two are told apart by the store's own oldest
record, which **Settings** shows beside the retention limits.

## How long requests took

A row per model: how many of its answers were timed, then the median, ninetieth
and ninety-ninth percentile of the time to the first token, and of the rate it
generated at afterwards.

Only answers count. A refusal has no latency to distribute and a request that
never streamed has no first token, so neither is in these figures — which is
why the count in this table is usually smaller than the model's request count
in the live view above. The rate is the tokens after the first over the time
spent generating them, which is what vLLM, LM Studio and the OpenTelemetry
conventions all mean by tokens a second.

The percentiles are nearest rank: the ninetieth of ten answers is the ninth of
them in order, so every figure shown is one a request actually produced rather
than an interpolation between two. A model with three answers behind it has
three percentiles that are almost the same request; the count beside them is
there to be read first.

What this table cannot show is *why* a request was slow. A model that had to be
loaded first spends seconds before it generates anything, and that shows up
here as a slow first token with nothing to say it was a load. The live table's
**Waited** column separates the two for a recent request, and the eviction
table below says how often loading happens at all.

## Time to first token, spread

The same times to first token, counted into buckets. It is the same data as the
percentiles above and it answers a different question: whether a model is
consistently quick, or quick most of the time and slow in a way the median
hides. Two clusters — one under a quarter of a second, one over five — usually
mean a model that keeps being evicted and reloaded rather than a model that is
slow.

## Evictions and reloads by hour

The local day, hour by hour: how many models were evicted to make room for
another, and how many model servers were started.

Only an eviction counts as an eviction. A model reaped after sitting idle, one
you unloaded yourself, one that crashed, and every model server stopped when
Gropius quits are all removals, and counting them here would say the memory
budget is thrashing when nothing of the kind happened. The store records the
reason for exactly this distinction, and the
[reference page](statistics-store-reference.md) lists every reason it can hold.

The started column counts every model server Gropius starts, including one that
never became ready, because the cost this table is about is the loading, which
a server that failed still spent. It cannot say why a model was loaded: Gropius
knows only that something asked for it.

## What none of the views can show

**Anything that happened while the switch was off.** The tables draw on the
records and on nothing else, so a week when recording was off is a week with no
rows — indistinguishable, in the tables alone, from a week you served nothing.
This is the point of the opt-in rather than a shortcoming of the views.

**Further back than the records reach.** Two limits bound the store, a number
of months and a size cap, and the size cap is the hard one. When the store
passes it the oldest file goes, and the days in it go with it. A day kept only
as a coarse per-model daily total is marked **daily total only** in the
tokens-per-day table, so exact rows and summary totals are never mixed without
saying so.

**Who sent anything.** No client address is recorded, so every table is per
model and per period and never says who: not which machine on your network,
not which account on this Mac. On a Mac several people share, the records cover
every request the server handled and still say nothing about whose they were.

**What was asked or answered.** No prompt, no completion, no API key. The part
of Gropius that keeps these figures is never handed a request, its headers or
its connection, so there is nothing of the kind for a table to show.
