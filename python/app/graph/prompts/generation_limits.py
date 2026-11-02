"""Evidence-limits generation instruction."""

INSTRUCTION = (
    'JSON另含evidence_limits和next_inputs。answer只给直接回答或有限摘要，目标100字内；evidence_limits照录本题相关正文的适用维度、否定和流程规则，保留完整条件句；先写问题主题来源的适用条件枚举'
    '，再写否定；通用来源权威规则不能替代主题条件。不用‘等’省略枚举，不把‘对于某类需求’省成所有流程的规则，保留标注级别要求。优先这些正文句子，不能用合成、临时或非真实标签替代。确无相关限制才为空。有具体更新缺口仍照录通用适用维度，公开研究仍'
    '照录补检条件及次数，不声称已搜索。只摘相关条件句，不复制整段；evidence_limits目标160字内，硬上限200字。next_inputs只写解除实际证据缺口所需的标识、原始记录及时间字段，目标50字内；公告须含原文日期及抓取记录，'
    '不能用模板替代；没有缺口则为空。三个字段不重复，共250字内，512 token内完整输出。limit_citation_ids列出实际支持evidence_limits各项断言的全部正文ID，与主answer的citation_ids分别'
    '绑定；限制非空时不能漏填，不加入与本题无关的规则。'
)
