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
