from __future__ import annotations

import pytest

from app import metrics


def test_metrics_render_has_counter_type_and_value():
    metrics.usage_log_write_failure()
    body = metrics.render()
    assert "# TYPE usage_log_write_failures_total counter" in body
    assert "usage_log_write_failures_total " in body
    assert "password" not in body and "dsn" not in body.lower()


def test_metrics_address_requires_loopback_and_valid_port():
    assert metrics.validate_metrics_address("127.0.0.1:9091") == ("127.0.0.1", 9091)
    assert metrics.validate_metrics_address("localhost:9091") == ("127.0.0.1", 9091)
    for address in ("0.0.0.0:9091", "192.0.2.10:9091", "127.0.0.1:0", "127.0.0.1:bad"):
        with pytest.raises(ValueError):
            metrics.validate_metrics_address(address)
