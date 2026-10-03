const { test } = require('node:test');
const assert = require('node:assert/strict');
const { consume } = require('../website/assets/chat-stream.js');

test('每個分割位置都保留混合換行、多行 data、UTF-8 與 DONE 後停止', async () => {
  const text = ': heartbeat\r\n\n' + 'data: {"content":\n' + 'data: "中文😀"}\n\r\n' +
    'event: ignored\r\ndata: {"usage":3}\r\n\r\n' + 'data: [DONE]\n\n' + 'data: invalid\n\n';
  const bytes=new TextEncoder().encode(text);
  for(let split=0;split<=bytes.length;split++) {
    const stream=new ReadableStream({start(controller){controller.enqueue(bytes.slice(0,split));controller.enqueue(bytes.slice(split));controller.close();}});
    const events=[];
    await consume(stream,event=>events.push(event));
    assert.deepEqual(events,[{content:'中文😀'},{usage:3}],`split=${split}`);
    assert(!stream.locked);
  }
});

test('未結束的大型 event 不會誤認 EOF 為完成', async () => {
  const text='data: {"content":"'+ '字'.repeat(32768);
  const bytes=new TextEncoder().encode(text);
  let position=0;
  const stream=new ReadableStream({pull(controller){
    if(position>=bytes.length) {controller.close();return;}
    controller.enqueue(bytes.subarray(position,position+37));position+=37;
  }});
  await assert.rejects(consume(stream,()=>assert.fail('不應輸出未完成事件')),/回答未完成/);
  assert(!stream.locked);
});
