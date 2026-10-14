<script setup lang="ts">
import {reactive,ref} from 'vue'
import {post} from './api'
const props=defineProps<{shop:Record<string,any>;revision:string}>();const emit=defineEmits<{saved:[]}>()
const value=reactive({name:props.shop.name,public_slug:props.shop.public_slug||'my-shop',welcome_message:props.shop.welcome_message,customer_chat_enabled:props.shop.customer_chat_enabled});const busy=ref(false);const notice=ref('')
async function save(){busy.value=true;try{await post(`/admin/v1/merchant/shops/${encodeURIComponent(props.shop.shop_id)}/settings`,{...value,permission_revision:props.revision});notice.value='店铺设置已保存。';emit('saved')}catch(e){notice.value=e instanceof Error?e.message:'保存失败。'}finally{busy.value=false}}
</script>
<template><form class="import-form" @submit.prevent="save"><label>店铺名称<input v-model="value.name" maxlength="100" required></label><label>客户入口标识<input v-model="value.public_slug" pattern="[a-z][a-z0-9-]{0,62}" required></label><label>欢迎语<textarea v-model="value.welcome_message" maxlength="500"/></label><label><input type="checkbox" v-model="value.customer_chat_enabled">启用客户咨询</label><button class="primary" :disabled="busy">保存设置</button><p role="status">{{notice}}</p></form></template>
