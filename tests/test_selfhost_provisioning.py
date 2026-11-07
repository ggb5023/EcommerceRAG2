import importlib.util
from pathlib import Path

import pytest

ROOT=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('selfhost_tool',ROOT/'scripts/ops/selfhost.py')
tool=importlib.util.module_from_spec(spec);spec.loader.exec_module(tool)


def test_loopback_origin_uses_an_ip_san():
    assert tool.local_certificate_san('127.0.0.1')=='IP:127.0.0.1'
    assert tool.local_certificate_san('::1')=='IP:::1'


def test_dns_origin_keeps_a_dns_san():
    assert tool.local_certificate_san('merchant.example.test')=='DNS:merchant.example.test'


@pytest.mark.parametrize('value',[0,65536,'not-a-port'])
def test_listen_port_is_validated(value):
    with pytest.raises(ValueError):
        tool.parse_listen_port(value)


def test_listen_port_accepts_container_port():
    assert tool.parse_listen_port('18443')==18443
