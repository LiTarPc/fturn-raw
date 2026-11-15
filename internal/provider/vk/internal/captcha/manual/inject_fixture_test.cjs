// Offline script fixture: no browser/network/API requests. Run with Node.js.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const { createHash } = require('node:crypto');
const code = fs.readFileSync(__dirname + '/inject.js', 'utf8');
function realm(capture,initial) {
 const posts=[];
 class XHR {open(){} send(){} addEventListener(){}}
 class Form {submit(){}}
 const document={body:{style:{},append(){},innerHTML:''},documentElement:{},querySelectorAll(){return[]},addEventListener(){},createElement(){return {style:{},append(){}}}};
 const c={document,navigator:{},XMLHttpRequest:XHR,HTMLFormElement:Form,MutationObserver:class{observe(){}},location:{protocol:'http:'},Blob,URL,Request,URLSearchParams,btoa:s=>Buffer.from(s,'binary').toString('base64'),setTimeout(){},atob:s=>Buffer.from(s,'base64').toString('binary'),fetch:(url,options)=>{posts.push({url,body:options?.body});return Promise.resolve({ok:true,clone(){return{json:()=>Promise.resolve({response:{success_token:'PRIVATE_TOKEN'}})}}})},open(){},close(){}};
 c.window=c;c.__ftpCaptcha={local:'http://localhost:8765',upstream:'https://id.vk.ru',capturePow:capture,powPath:'/local-captcha-pow/FIXTURE_NONCE'};
 if(initial!==undefined)c.captchaPowResult=initial;
 vm.createContext(c);vm.runInContext(code,c);return{c,posts};
}
function proof(input){let nonce=0,hash;do{hash=createHash('sha256').update(input+nonce).digest('hex');if(hash.startsWith('0'))break;nonce++;}while(nonce<100000);return 'v2.'+Buffer.from(JSON.stringify({hash,nonce,telemetry:{ua:{ok:true,result:{userAgent:'Fixture WebView'}}}})).toString('base64');}
(async()=>{
 const value=proof('fixture-nonce');
 let {c,posts}=realm(true);
 c.captchaPowResult='v2.'+Buffer.from(JSON.stringify({error:'pending'})).toString('base64');
 assert.equal(posts.length,0);
 c.captchaPowResult=value;
 c.captchaPowResult=value;
 await Promise.resolve();
 assert.equal(posts.length,1);
 assert.equal(posts[0].url,'/local-captcha-pow/FIXTURE_NONCE');
 assert.equal(new URLSearchParams(posts[0].body).get('pow'),value);
 await c.fetch('https://api.vk.ru/method/captchaNotRobot.check');
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(posts.filter(x=>x.url==='/local-captcha-result').length,0);
 ({posts}=realm(true,value));await Promise.resolve();assert.equal(posts.length,1);
 ({c,posts}=realm(false));c.captchaPowResult=value;await Promise.resolve();assert.equal(posts.length,0);
 await c.fetch('https://api.vk.ru/method/captchaNotRobot.check');await new Promise(resolve=>setImmediate(resolve));assert.equal(posts.filter(x=>x.url==='/local-captcha-result').length,1);
 console.log('PASS injector: final browser proof captured once; incomplete ignored; initial final proof captured; capture cannot return token; legacy token route preserved.');
})().catch(error=>{console.error('FAIL offline injector fixture',error);process.exitCode=1;});
