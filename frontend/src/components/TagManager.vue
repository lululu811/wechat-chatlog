<template>
  <div class="bg-white dark:bg-[#121215] rounded-2xl border border-zinc-200/80 dark:border-zinc-800/80 p-6 shadow-xs space-y-6">
    <div class="space-y-1">
      <h3 class="text-base font-bold text-zinc-900 dark:text-zinc-100">分类标签管理</h3>
      <p class="text-xs text-zinc-400">
        标签用于公众号分类，「动态」页可按标签快速筛选文章。删除标签只会解除与公众号的关联，不会删除公众号或历史文章。
      </p>
    </div>

    <!-- Tag List -->
    <div class="divide-y divide-zinc-100 dark:divide-zinc-800/60">
      <div 
        v-for="t in tags" 
        :key="t.id"
        class="py-3 flex items-center justify-between gap-4 hover:bg-zinc-50/50 dark:hover:bg-zinc-900/30 px-2 rounded-xl transition-colors"
      >
        <!-- Normal display -->
        <div v-if="editingId !== t.id" class="flex items-center gap-3 min-w-0">
          <span class="w-3.5 h-3.5 rounded-full flex-shrink-0 shadow-xs" :style="{ backgroundColor: t.color }"></span>
          <span class="text-sm font-semibold text-zinc-900 dark:text-zinc-100 truncate">{{ t.name }}</span>
          <span class="text-xs text-zinc-400 font-mono">({{ tagCounts[t.id] || 0 }} 个公众号)</span>
        </div>

        <!-- Edit display -->
        <div v-else class="flex items-center gap-2 flex-1">
          <input 
            type="color" 
            v-model="editColor"
            class="w-7 h-7 rounded border border-zinc-200 dark:border-zinc-700 cursor-pointer p-0.5 bg-transparent"
          />
          <input 
            type="text" 
            v-model="editName"
            class="text-xs px-2.5 py-1.5 rounded-lg border border-zinc-200 dark:border-zinc-700 bg-transparent text-zinc-900 dark:text-zinc-100 flex-1 max-w-xs focus:ring-1 focus:ring-emerald-500 outline-none"
          />
          <button @click="saveEdit(t.id)" class="text-xs px-2.5 py-1 rounded-lg bg-emerald-600 text-white font-medium shadow-xs">保存</button>
          <button @click="editingId = null" class="text-xs px-2 py-1 text-zinc-400 hover:text-zinc-600 dark:hover:text-zinc-200">取消</button>
        </div>

        <!-- Actions -->
        <div v-if="editingId !== t.id" class="flex items-center gap-3">
          <button @click="startEdit(t)" class="text-xs text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200 transition-colors">
            编辑
          </button>
          <button @click="confirmDelete(t)" class="text-xs text-rose-500 hover:text-rose-600 dark:hover:text-rose-400 transition-colors">
            删除
          </button>
        </div>
      </div>
    </div>

    <!-- Add Tag Row -->
    <div class="pt-4 border-t border-zinc-100 dark:border-zinc-800/80 flex items-center gap-3">
      <input 
        type="color" 
        v-model="newColor"
        class="w-9 h-9 rounded-xl border border-zinc-200 dark:border-zinc-700 cursor-pointer p-0.5 bg-transparent"
        title="选择标签颜色"
      />
      <input 
        type="text" 
        v-model="newName" 
        placeholder="新标签名称…"
        @keyup.enter="createTag"
        class="text-xs px-3.5 py-2 rounded-xl border border-zinc-200 dark:border-zinc-800 bg-zinc-50 dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100 placeholder-zinc-400 flex-1 max-w-xs focus:border-emerald-500 outline-none shadow-xs"
      />
      <button 
        @click="createTag"
        :disabled="!newName.trim()"
        class="text-xs px-4 py-2 rounded-xl bg-emerald-600 hover:bg-emerald-500 text-white font-medium disabled:opacity-50 transition-colors shadow-xs"
      >
        添加标签
      </button>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue';
import { api } from '@/api/client';
import { useToast } from '@/composables/useToast';

const props = defineProps({
  tags: Array,
  tagCounts: {
    type: Object,
    default: () => ({}),
  },
});

const emit = defineEmits(['tags-updated']);
const toast = useToast();

const newName = ref('');
const newColor = ref('#059669');

const editingId = ref(null);
const editName = ref('');
const editColor = ref('');

function startEdit(t) {
  editingId.value = t.id;
  editName.value = t.name;
  editColor.value = t.color;
}

async function saveEdit(id) {
  if (!editName.value.trim()) return;
  try {
    await api.updateTag(id, editName.value.trim(), editColor.value);
    toast.success('标签更新成功');
    editingId.value = null;
    emit('tags-updated');
  } catch (err) {
    toast.error('更新失败: ' + err.message);
  }
}

async function confirmDelete(t) {
  const count = props.tagCounts[t.id] || 0;
  const msg = `确认删除标签「${t.name}」吗？\n该标签正被 ${count} 个公众号使用，删除后将一并解除关联。`;
  if (!confirm(msg)) return;

  try {
    await api.deleteTag(t.id);
    toast.success('标签已删除');
    emit('tags-updated');
  } catch (err) {
    toast.error('删除失败: ' + err.message);
  }
}

async function createTag() {
  if (!newName.value.trim()) return;
  try {
    await api.createTag(newName.value.trim(), newColor.value);
    toast.success('标签创建成功');
    newName.value = '';
    emit('tags-updated');
  } catch (err) {
    toast.error('创建失败: ' + err.message);
  }
}
</script>
