from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path
from threading import Event

from app.providers import build_provider
from app.providers.aliyun_bailian import AlibabaBailianProvider, HTTPResponse
from app.providers.config import (
    ProviderConfigError,
    load_provider_config,
    validate_values,
)
from app.providers.contracts import ProviderError

BASE_VALUES = {
    "PROVIDER_PROFILE": "aliyun-bailian",
    "BAILIAN_ENDPOINT": "https://dashscope.example.test",
    "BAILIAN_REGION": "cn-beijing",
    "CONTROL_MODEL": "control-model",
    "EMBEDDING_MODEL": "embedding-model",
    "RERANK_MODEL": "rerank-model",
    "GENERATION_MODEL": "generation-model",
    "EMBEDDING_DIMENSIONS": "1024",
    "PROVIDER_TIMEOUT_S": "5",
    "PROVIDER_QUOTA_RPM": "10",
    "DASHSCOPE_API_KEY": "secret-key-for-test",
}


def response(
    payload: dict, *, status: int = 200, headers: dict[str, str] | None = None
) -> HTTPResponse:
    return HTTPResponse(
        status,
        headers or {"x-request-id": "header-request-id"},
        json.dumps(payload).encode("utf-8"),
    )


class FakeTransport:
    def __init__(self, responses: list[HTTPResponse | BaseException]):
        self.responses = list(responses)
        self.calls: list[dict[str, object]] = []

    async def request(
        self,
        method: str,
        url: str,
        *,
        headers: dict[str, str],
        body: bytes,
        timeout_s: float,
        cancel_event: object | None = None,
    ) -> HTTPResponse:
        self.calls.append(
            {
                "method": method,
                "url": url,
                "headers": headers,
                "body": json.loads(body),
                "timeout_s": timeout_s,
            }
        )
        item = self.responses.pop(0)
        if isinstance(item, BaseException):
            raise item
        return item


class ProviderConfigTests(unittest.TestCase):
    def test_mock_profile_is_explicit_and_does_not_need_a_key(self):
        values = {
            key: value
            for key, value in BASE_VALUES.items()
            if key != "DASHSCOPE_API_KEY"
        }
        values["PROVIDER_PROFILE"] = "mock"
        values.pop("BAILIAN_ENDPOINT")
        values.pop("BAILIAN_REGION")
        config = validate_values(values)
        self.assertEqual(config.profile, "mock")
        self.assertIsNone(config.api_key)

    def test_missing_secret_or_wrong_dimension_blocks_cloud_profile(self):
        for field, value in (
            ("DASHSCOPE_API_KEY", None),
            ("EMBEDDING_DIMENSIONS", "768"),
        ):
            values = dict(BASE_VALUES)
            if value is None:
                values.pop(field)
            else:
                values[field] = value
            with self.subTest(field=field), self.assertRaises(ProviderConfigError):
                validate_values(values)

    def test_file_loader_does_not_merge_process_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "providers.env"
            path.write_text(
                "\n".join(f"{key}={value}" for key, value in BASE_VALUES.items()) + "\n"
            )
            config = load_provider_config(path)
        self.assertEqual(config.api_key, "secret-key-for-test")
        self.assertEqual(config.timeout_s, 5.0)


class AlibabaProviderTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.config = validate_values(BASE_VALUES)

    def make_provider(
        self, responses: list[HTTPResponse | BaseException]
    ) -> tuple[AlibabaBailianProvider, FakeTransport]:
        transport = FakeTransport(responses)
        return AlibabaBailianProvider(self.config, transport, test_mode=True), transport

    async def test_four_slots_parse_and_keep_secret_out_of_payload(self):
        dense = [0.25] * 1024
        provider, transport = self.make_provider(
            [
                response(
                    {
                        "id": "control-id",
                        "choices": [
                            {
                                "message": {"content": '{"intent":"product"}'},
                                "finish_reason": "stop",
                            }
                        ],
                        "usage": {"prompt_tokens": 2, "completion_tokens": 3},
                    }
                ),
                response(
                    {
                        "request_id": "embed-id",
                        "output": {
                            "embeddings": [
                                {
                                    "embedding": dense,
                                    "sparse_embedding": [
                                        {"token_id": 4, "weight": 0.8},
                                        {"index": 9, "value": 0.2},
                                    ],
                                }
                            ]
                        },
                    }
                ),
                response(
                    {
                        "request_id": "rerank-id",
                        "output": {
                            "results": [
                                {"index": 1, "relevance_score": 0.9},
                                {"index": 0, "relevance_score": 0.1},
                            ]
                        },
                    }
                ),
                response(
                    {
                        "request_id": "generation-id",
                        "choices": [
                            {"message": {"content": "OK"}, "finish_reason": "stop"}
                        ],
                    }
                ),
            ]
        )

        control = await provider.control(
            [{"role": "user", "content": "classify"}],
            response_schema={"name": "control", "schema": {"type": "object"}},
        )
        embeddings = await provider.embed(
            ["text"], text_type="query", output_type="dense&sparse"
        )
        rerank = await provider.rerank("query", ["a", "b"], top_n=2)
        generation = await provider.generate([{"role": "user", "content": "answer"}])

        self.assertEqual(control.structured["intent"], "product")
        self.assertEqual(control.usage.total_tokens, 5)
        self.assertEqual(len(embeddings[0].dense), 1024)
        self.assertEqual([item.token_id for item in embeddings[0].sparse], [4, 9])
        self.assertEqual(rerank.items[0].index, 1)
        self.assertEqual(generation.text, "OK")
        self.assertEqual(len(transport.calls), 4)
        self.assertTrue(
            all(
                call["headers"]["Authorization"] == "Bearer secret-key-for-test"
                for call in transport.calls
            )
        )
        self.assertTrue(
            all(
                "secret-key-for-test" not in json.dumps(call["body"])
                for call in transport.calls
            )
        )
        self.assertEqual(
            transport.calls[0]["url"],
            "https://dashscope.example.test/compatible-mode/v1/chat/completions",
        )
        self.assertEqual(transport.calls[0]["body"]["response_format"], {
            "type": "json_schema", "json_schema": {
                "name": "control", "strict": True, "schema": {"type": "object"},
            },
        })

    async def test_raw_schemas_are_wrapped_and_invalid_schema_never_sends(self):
        for slot in ("control", "generation"):
            provider, transport = self.make_provider([response({
                "request_id": "schema-test", "choices": [{
                    "message": {"content": '{"answer":"supported"}'}, "finish_reason": "stop",
                }],
            })])
            schema = {"type": "object", "properties": {"answer": {"type": "string"}}, "required": ["answer"]}
            await getattr(provider, "control" if slot == "control" else "generate")(
                [{"role": "user", "content": "controlled input"}], response_schema=schema)
            self.assertEqual(transport.calls[0]["body"]["response_format"], {
                "type": "json_schema", "json_schema": {"name": slot, "strict": True, "schema": schema},
            })
            self.assertNotIn("name", schema)
        for schema in ({"type": "array"}, {"name": "bad name", "schema": {"type": "object"}}):
            provider, transport = self.make_provider([])
            with self.assertRaises(ProviderError) as raised:
                await provider.control([{"role": "user", "content": "controlled input"}], response_schema=schema)
            self.assertEqual(raised.exception.code, "invalid_input")
            self.assertEqual(transport.calls, [])

    async def test_structured_output_and_response_errors_are_classified(self):
        provider, _ = self.make_provider(
            [
                response(
                    {
                        "request_id": "bad",
                        "choices": [
                            {
                                "message": {"content": "not-json"},
                                "finish_reason": "stop",
                            }
                        ],
                    }
                ),
            ]
        )
        with self.assertRaisesRegex(ProviderError, "structured output") as raised:
            await provider.control([{"role": "user", "content": "x"}])
        self.assertEqual(raised.exception.code, "schema_error")

        provider, _ = self.make_provider([response({}, status=429), response({}, status=429)])
        with self.assertRaises(ProviderError) as raised:
            await provider.generate([{"role": "user", "content": "x"}])
        self.assertEqual(raised.exception.code, "rate_limited")
        self.assertTrue(raised.exception.retryable)

        provider, _ = self.make_provider([response({}, status=503), response({}, status=503)])
        with self.assertRaises(ProviderError) as raised:
            await provider.generate([{"role": "user", "content": "x"}])
        self.assertEqual(raised.exception.code, "upstream_error")

    async def test_embedding_and_rerank_integrity_checks(self):
        provider, _ = self.make_provider(
            [
                response(
                    {
                        "request_id": "bad",
                        "output": {
                            "embeddings": [
                                {
                                    "embedding": [0.1] * 1023,
                                    "sparse_embedding": [
                                        {"token_id": 1, "weight": 0.2}
                                    ],
                                }
                            ]
                        },
                    }
                )
            ]
        )
        with self.assertRaisesRegex(ProviderError, "1024"):
            await provider.embed(["x"], text_type="document")

        provider, _ = self.make_provider(
            [
                response(
                    {
                        "request_id": "bad",
                        "output": {
                            "embeddings": [
                                {"embedding": [0.1] * 1024, "sparse_embedding": []}
                            ]
                        },
                    }
                )
            ]
        )
        with self.assertRaisesRegex(ProviderError, "sparse output"):
            await provider.embed(["x"], text_type="document")

        for rows in (
            [
                {"index": 0, "relevance_score": 0.1},
                {"index": 0, "relevance_score": 0.2},
            ],
            [
                {"index": 2, "relevance_score": 0.2},
                {"index": 1, "relevance_score": 0.1},
            ],
            [
                {"index": 0, "relevance_score": 1.1},
                {"index": 1, "relevance_score": 0.1},
            ],
        ):
            provider, _ = self.make_provider(
                [response({"request_id": "bad", "output": {"results": rows}})]
            )
            with self.subTest(rows=rows), self.assertRaises(ProviderError) as raised:
                await provider.rerank("q", ["a", "b"], top_n=2)
            self.assertEqual(raised.exception.code, "invalid_response")

    async def test_timeout_and_cancellation_are_stable(self):
        provider, _ = self.make_provider([TimeoutError(), TimeoutError()])
        with self.assertRaises(ProviderError) as raised:
            await provider.generate([{"role": "user", "content": "x"}])
        self.assertEqual(raised.exception.code, "timeout")
        self.assertTrue(raised.exception.retryable)

        cancelled = Event()
        cancelled.set()
        provider, transport = self.make_provider([])
        with self.assertRaises(ProviderError) as raised:
            await provider.generate(
                [{"role": "user", "content": "x"}], cancel_event=cancelled
            )
        self.assertEqual(raised.exception.code, "cancelled")
        self.assertFalse(transport.calls)


class MockProviderTests(unittest.IsolatedAsyncioTestCase):
    async def test_mock_covers_all_slots_without_network(self):
        values = dict(BASE_VALUES)
        values["PROVIDER_PROFILE"] = "mock"
        values.pop("DASHSCOPE_API_KEY")
        values.pop("BAILIAN_ENDPOINT")
        values.pop("BAILIAN_REGION")
        provider = build_provider(validate_values(values))
        self.assertTrue(provider.is_mock)
        self.assertEqual(
            (await provider.control([{"role": "user", "content": "x"}])).finish_reason,
            "stop",
        )
        for output_type in ("dense", "sparse", "dense&sparse"):
            result = (
                await provider.embed(["x"], text_type="query", output_type=output_type)
            )[0]
            self.assertEqual(len(result.dense), 1024 if output_type != "sparse" else 0)
            self.assertTrue(
                result.sparse if output_type != "dense" else not result.sparse
            )
        self.assertEqual(
            len((await provider.rerank("x", ["x", "y"], top_n=1)).items), 1
        )
        generated = await provider.generate(
            [{"role": "user", "content": "draft"}], response_schema={"type": "object"}
        )
        self.assertEqual(generated.structured["answer"], "draft")


if __name__ == "__main__":
    unittest.main()


def test_structured_boundary_enforces_declared_bounds_enums_and_extra_fields():
    import pytest
    from app.providers.aliyun_bailian import _structured
    from app.providers.contracts import ProviderError
    schema = {'type': 'object', 'additionalProperties': False, 'required': ['answer', 'ids'],
              'properties': {'answer': {'type': 'string', 'minLength': 1, 'maxLength': 4},
                             'ids': {'type': 'array', 'maxItems': 2, 'uniqueItems': True,
                                     'items': {'type': 'string', 'enum': ['a', 'b']}}}}
    assert _structured('{"answer":"ok","ids":["a"]}', 'generation', schema)['answer'] == 'ok'
    for payload in ['{"answer":"too long","ids":[]}', '{"answer":"","ids":[]}',
                    '{"answer":"ok","ids":["outside"]}', '{"answer":"ok","ids":["a","a"]}',
                    '{"answer":"ok","ids":["a","b","a"]}', '{"answer":"ok","ids":[],"extra":1}']:
        with pytest.raises(ProviderError) as error:
            _structured(payload, 'generation', schema)
        assert error.value.code == 'schema_error' and error.value.retryable is False


def test_schema_diagnostic_reports_constraint_without_response_or_property_text(caplog):
    import hashlib

    import pytest
    from app.providers.aliyun_bailian import _structured
    from app.providers.contracts import ProviderError

    payload = '{"private-field":"private-response"}'
    schema = {'type': 'object', 'properties': {'private-field': {'type': 'string', 'maxLength': 2}}}
    with pytest.raises(ProviderError):
        _structured(payload, 'control', schema)
    assert 'root/property[0]:maxLength' in caplog.text
    assert hashlib.sha256(payload.encode()).hexdigest() in caplog.text
    assert 'private-field' not in caplog.text and 'private-response' not in caplog.text


def test_free_only_allocation_is_nonretryable_quota_not_bad_credentials():
    import pytest
    from app.providers.aliyun_bailian import HTTPResponse, _json
    from app.providers.contracts import ProviderError
    for payload in [b'{"code":"AllocationQuota.FreeTierOnly","message":"restricted"}',
                    b'{"error":{"code":"AllocationQuota.FreeTierOnly","message":"restricted"}}']:
        with pytest.raises(ProviderError) as error:
            _json(HTTPResponse(403, {}, payload), 'generation')
        assert error.value.code == 'allocation_quota'
        assert error.value.retryable is False and error.value.status == 403
    with pytest.raises(ProviderError) as error:
        _json(HTTPResponse(403, {}, b'{"code":"InvalidApiKey"}'), 'generation')
    assert error.value.code == 'authentication'
