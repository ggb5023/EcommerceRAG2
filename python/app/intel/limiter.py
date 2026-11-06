"""Small synchronous token bucket for the intelligence worker."""
from __future__ import annotations

import threading
import time


class TokenBucket:
    def __init__(self, rpm: int, *, clock=time.monotonic, sleep=time.sleep) -> None:
        if not 1 <= rpm <= 60:
            raise ValueError("V2_INTEL_RPM must be between 1 and 60")
        self.capacity = float(rpm)
        self.rate = rpm / 60.0
        self.tokens = float(rpm)
        self.updated = clock()
        self.clock = clock
        self.sleep = sleep
        self.lock = threading.Lock()

    def _refill(self) -> None:
        now = self.clock()
        elapsed = max(0.0, now - self.updated)
        self.tokens = min(self.capacity, self.tokens + elapsed * self.rate)
        self.updated = now

    def acquire(self) -> None:
        with self.lock:
            self._refill()
            if self.tokens >= 1.0:
                self.tokens -= 1.0
                return
            wait = (1.0 - self.tokens) / self.rate
            self.sleep(wait)
            self.updated = self.clock()
            self.tokens = 0.0
