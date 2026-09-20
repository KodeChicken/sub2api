import type { PromptMaterial } from './types'

export const promptMaterials: PromptMaterial[] = [
  {
    id: 'product-clean',
    category: 'product',
    title: '纯净产品图',
    description: '适合电商主图和产品详情页',
    prompt: '生成一张高端商业产品摄影，主体居中，干净纯色背景，柔和棚拍光线，材质细节清晰，构图简洁，真实摄影质感。',
  },
  {
    id: 'food-editorial',
    category: 'food',
    title: '餐饮宣传图',
    description: '强调食物质感和自然光影',
    prompt: '生成一张精致餐饮宣传摄影，食物细节丰富，暖色自然光，浅景深，桌面布置克制，画面具有高级杂志编辑感。',
  },
  {
    id: 'event-poster',
    category: 'poster',
    title: '活动主视觉',
    description: '适合发布会、促销与活动海报',
    prompt: '生成一张现代活动主视觉海报，明确的视觉中心，留出标题和日期排版空间，层次清晰，高对比度，专业品牌设计质感。',
  },
  {
    id: 'social-lifestyle',
    category: 'social',
    title: '社交媒体配图',
    description: '轻松自然的生活方式画面',
    prompt: '生成一张自然真实的生活方式摄影，明亮环境光，人物状态松弛，色彩清爽，适合社交媒体发布，避免过度摆拍。',
  },
  {
    id: 'travel-cinematic',
    category: 'travel',
    title: '旅行电影感',
    description: '宽幅风景与电影级色彩',
    prompt: '生成一张电影感旅行风景摄影，宽广空间，真实天气和光线，细腻层次，克制的电影调色，具有明确的前景、中景和远景。',
  },
  {
    id: 'illustration-friendly',
    category: 'illustration',
    title: '友好插画',
    description: '适合产品说明和内容配图',
    prompt: '生成一张现代扁平插画，人物友好自然，形状简洁，色彩协调，背景留白充足，适合产品说明和内容配图。',
  },
]
