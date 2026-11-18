import forced_census


def finished(forced, findings, completion, low=0):
    return (
        'msg="deep-review agent finished" agent=general turns=4 lookups=1 '
        f"forced={forced} reasoning_low={low} reasoning_off=0 findings={findings} "
        f"prompt_tokens=10 completion_tokens={completion} reasoning_tokens=1"
    )


def test_counts_forced_agents_and_length_cuts_separately():
    lines = [
        finished("true", 0, 100, low=1),
        finished("false", 2, 50),
        'msg="a forced submission made no usable tool call" step=1 finish=length',
        'msg="a forced submission made no usable tool call" step=1 finish=stop',
    ]
    result = forced_census.census(lines)
    assert result["agents"] == 2 and result["forced"] == 1
    assert result["length_cut_forced_calls"] == 1
    assert result["agents_with_ladder_step_down"] == 1
    assert result["forced_with_zero_findings"] == 1
    assert result["cut_token_upper_bound"] == forced_census.FORCED_CAP


def test_no_agents_reports_no_rate_instead_of_zero():
    result = forced_census.census(["unrelated line"])
    assert result["agents"] == 0 and result["forced_rate"] is None


def test_counts_failed_agents_and_diff_only_fallbacks_from_logs():
    lines = [
        'level=WARN msg="a deep-review agent failed; keeping the other agents\' findings" agent=general',
        'level=WARN msg="every deep-review agent failed; reviewing the batch from the diff alone" agents=2',
        'level=WARN msg="unrelated error"',
    ]
    result = forced_census.census(lines)
    assert result["agents_lost"] == 1
    assert result["whole_batch_diff_only_fallbacks"] == 1
