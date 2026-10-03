const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const { test } = require('node:test');

function exercise(filename) {
  let renders = 0, mathRenders = 0;
  const element = () => ({ hidden: false, open: true, textContent: '', innerHTML: '',
    classList: { toggle() {} }, querySelectorAll: () => [] });
  const scroller = element();
  const context = {window: {
    LlamaLoader: {byId: () => scroller, t: value => value},
    markdownit: () => ({render: text => { renders++; return text; }}),
    renderMathInElement: () => { mathRenders++; }
  }};
  const source = fs.readFileSync(filename, 'utf8');
  const boundary = source.indexOf('  byId("chatForm").addEventListener');
  assert(boundary > 0);
  // 只移除啟動頁面時的事件掛鉤，保留正式的 Markdown／思考內容函式。
  vm.runInNewContext(source.slice(0, boundary) + 'globalThis.updateView = updateStreamingMessage; })();', context);
  const view = Object.fromEntries(['row','indicator','thinking','reasoningContent','message','meta'].map(k=>[k,element()]));
  const reasoning = '固定思考內容 $x^2$\n'.repeat(128);
  let answer = '';
  for(let i=0;i<300;i++) {
    answer += '答案';
    context.updateView(view,{content:answer,reasoning},false);
    assert.equal(view.message.innerHTML,answer);
    assert.equal(view.reasoningContent.innerHTML,reasoning.trim());
  }
  context.updateView(view,{content:answer,reasoning,usage:{completion_tokens:300}},true);
  assert.equal(view.thinking.open,false);
  assert.equal(view.message.hidden,false);
  assert.equal(view.meta.hidden,false);
  return { updates:301, renders, mathRenders, finalContent:answer, finalReasoning:reasoning.trim() };
}

if (require.main === module && process.argv[2]) {
  const result=exercise(process.argv[2]);
  console.log(JSON.stringify({updates:result.updates,markdown_calls:result.renders,math_calls:result.mathRenders,dom_state_verified:true}));
} else {
  test('回答增長時保留已排版的思考內容，完成時仍更新狀態與用量', () => {
    const result=exercise('website/assets/chat.js');
    assert.equal(result.renders,301);
    assert.equal(result.mathRenders,301);
  });
}
