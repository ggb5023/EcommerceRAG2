"""Delivery-capability generation instruction."""

INSTRUCTION = (
    'JSON另含delivery_capability_limit，单独说明已接入物流状态查询但未提供配送承诺时效能力，即使订单信息齐全也不能保证到货时间。不重复核验步骤。该字段目标40字内，硬上限80字；只写能力限制，不重复answer或追加'
    '核验建议。'
)
