import asyncio

import pytest

from app.chains.provider import build_provider_chains, require_scope


def test_require_scope_rejects_missing_tenant_and_shop():
    with pytest.raises(ValueError, match="tenant_id"):
        require_scope({"shop_id": "s"})
    with pytest.raises(ValueError, match="tenant_id"):
        require_scope({"tenant_id": "", "shop_id": "s"})
    with pytest.raises(ValueError, match="shop_id"):
        require_scope({"tenant_id": 1})


def test_require_scope_defaults_to_answer_and_accepts_intelligence():
    assert require_scope({"tenant_id": 1, "shop_id": "s"}) == "answer"
    assert require_scope({"tenant_id": 1, "shop_id": "s", "domain": ""}) == "answer"
    assert require_scope({"tenant_id": 1, "shop_id": "s", "domain": "answer"}) == "answer"
    assert require_scope({"tenant_id": 1, "shop_id": "s", "domain": "intelligence"}) == "intelligence"


def test_require_scope_rejects_unknown_domain():
    with pytest.raises(ValueError, match="domain"):
        require_scope({"tenant_id": 1, "shop_id": "s", "domain": "search"})

class Provider:
    async def control(self,*a,**k): raise AssertionError
    async def embed(self,*a,**k): raise AssertionError
    async def rerank(self,*a,**k): raise AssertionError
    async def generate(self,*a,**k): raise AssertionError

def test_each_chain_rejects_missing_tenant():
    chains=build_provider_chains(control=Provider(),embedding=Provider(),rerank=Provider(),generation=Provider())
    for run in (chains.control,chains.embedding,chains.rerank,chains.generation):
        with pytest.raises(ValueError, match="tenant_id"):
            asyncio.run(run.ainvoke({"shop_id":"s"}))


def test_each_chain_rejects_unknown_domain():
    chains=build_provider_chains(control=Provider(),embedding=Provider(),rerank=Provider(),generation=Provider())
    for run in (chains.control,chains.embedding,chains.rerank,chains.generation):
        with pytest.raises(ValueError, match="domain"):
            asyncio.run(run.ainvoke({"tenant_id":1,"shop_id":"s","domain":"search"}))
