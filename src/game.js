'use strict';
/* Арена — 3D-шутер на чистом WebGL. Карта, оружие и счёт приходят с сервера мессенджера. */
const $=s=>document.querySelector(s);
const LS='shepot.v1';
let SES=null;try{SES=JSON.parse(localStorage.getItem(LS)||'null')}catch(e){}
if(!SES||!SES.token)location.replace('/');
const coarse=matchMedia('(pointer:coarse)').matches;
const cv=$('#c'),hud=$('#h'),hx=hud.getContext('2d');
const gl=cv.getContext('webgl',{antialias:!coarse,alpha:false,powerPreference:'high-performance'});
function fatal(t){const e=$('#err');e.style.display='flex';e.innerHTML=t}
if(!gl)fatal('Браузер не поддерживает WebGL. Обновите браузер или включите аппаратное ускорение.');

/* ---------- математика ---------- */
const M4=()=>{const m=new Float32Array(16);m[0]=m[5]=m[10]=m[15]=1;return m};
function mul(a,b){const o=new Float32Array(16);for(let c=0;c<4;c++)for(let r=0;r<4;r++)o[c*4+r]=a[r]*b[c*4]+a[4+r]*b[c*4+1]+a[8+r]*b[c*4+2]+a[12+r]*b[c*4+3];return o}
function persp(fy,as,n,f){const m=new Float32Array(16),t=1/Math.tan(fy/2);m[0]=t/as;m[5]=t;m[10]=(f+n)/(n-f);m[11]=-1;m[14]=2*f*n/(n-f);return m}
function T(m,x,y,z){for(let i=0;i<4;i++)m[12+i]+=m[i]*x+m[4+i]*y+m[8+i]*z;return m}
function RY(m,a){const c=Math.cos(a),s=Math.sin(a);for(let i=0;i<4;i++){const x=m[i],z=m[8+i];m[i]=x*c-z*s;m[8+i]=x*s+z*c}return m}
function RX(m,a){const c=Math.cos(a),s=Math.sin(a);for(let i=0;i<4;i++){const y=m[4+i],z=m[8+i];m[4+i]=y*c+z*s;m[8+i]=-y*s+z*c}return m}
function RZ(m,a){const c=Math.cos(a),s=Math.sin(a);for(let i=0;i<4;i++){const x=m[i],y=m[4+i];m[i]=x*c+y*s;m[4+i]=-x*s+y*c}return m}
function SC(m,x,y,z){for(let i=0;i<4;i++){m[i]*=x;m[4+i]*=y;m[8+i]*=z}return m}
const sub=(a,b)=>[a[0]-b[0],a[1]-b[1],a[2]-b[2]],add=(a,b)=>[a[0]+b[0],a[1]+b[1],a[2]+b[2]],scl=(a,s)=>[a[0]*s,a[1]*s,a[2]*s];
const dot=(a,b)=>a[0]*b[0]+a[1]*b[1]+a[2]*b[2],len=a=>Math.hypot(a[0],a[1],a[2]),nrm=a=>{const l=len(a)||1;return[a[0]/l,a[1]/l,a[2]/l]};
const lerp=(a,b,t)=>a+(b-a)*t,clamp=(x,a,b)=>Math.max(a,Math.min(b,x));
function angLerp(a,b,t){let d=b-a;while(d>Math.PI)d-=2*Math.PI;while(d<-Math.PI)d+=2*Math.PI;return a+d*t}
const fwdOf=(y,p)=>[-Math.sin(y)*Math.cos(p),Math.sin(p),-Math.cos(y)*Math.cos(p)];
const hex=h=>{const n=parseInt(h.slice(1),16);return[(n>>16&255)/255,(n>>8&255)/255,(n&255)/255]};

/* ---------- шейдер ---------- */
const VS=`attribute vec3 aP;attribute vec3 aN;attribute vec2 aT;attribute vec3 aC;
uniform mat4 uVP;uniform mat4 uM;uniform vec2 uScroll;
varying vec3 vW;varying vec3 vN;varying vec3 vC;varying vec2 vT;
void main(){vec4 w=uM*vec4(aP,1.0);vW=w.xyz;vN=(uM*vec4(aN,0.0)).xyz;vC=aC;vT=aT+uScroll;gl_Position=uVP*w;}`;
const FS=`precision mediump float;
uniform sampler2D uTex;uniform vec4 uTint;uniform float uEm;uniform vec3 uCam;uniform vec3 uFogC;uniform float uFogD;
uniform vec4 uLP[4];uniform vec3 uLC[4];uniform float uSky;uniform vec3 uZen;uniform vec3 uSunD;uniform float uSunK;
varying vec3 vW;varying vec3 vN;varying vec3 vC;varying vec2 vT;
void main(){if(uSky>0.5){vec3 d=normalize(vW-uCam);vec3 c=mix(uFogC,uZen,clamp(d.y*1.8,0.0,1.0));float s=max(dot(d,-uSunD),0.0);c+=vec3(1.0,0.92,0.75)*(pow(s,500.0)*2.0+pow(s,10.0)*0.22)*uSunK;gl_FragColor=vec4(c,1.0);return;}
vec4 t=texture2D(uTex,vT)*uTint;vec3 l=vC;vec3 n=normalize(vN);
for(int i=0;i<4;i++){float r=uLP[i].w;if(r>0.0){vec3 d=uLP[i].xyz-vW;float ds=length(d);float a=max(0.0,1.0-ds/r);l+=uLC[i]*a*a*(0.35+0.65*max(0.0,dot(n,d/max(ds,0.001))));}}
vec3 c=mix(t.rgb*l,t.rgb,uEm);float dz=length(vW-uCam)*uFogD;float f=1.0-exp(-dz*dz);
gl_FragColor=vec4(mix(c,uFogC,clamp(f,0.0,0.85)),t.a);}`;
let prog,A={},U={};
function initGL(){
  const sh=(t,s)=>{const o=gl.createShader(t);gl.shaderSource(o,s);gl.compileShader(o);if(!gl.getShaderParameter(o,gl.COMPILE_STATUS))throw new Error(gl.getShaderInfoLog(o));return o};
  prog=gl.createProgram();gl.attachShader(prog,sh(gl.VERTEX_SHADER,VS));gl.attachShader(prog,sh(gl.FRAGMENT_SHADER,FS));gl.linkProgram(prog);
  if(!gl.getProgramParameter(prog,gl.LINK_STATUS))throw new Error(gl.getProgramInfoLog(prog));
  gl.useProgram(prog);
  for(const a of['aP','aN','aT','aC']){A[a]=gl.getAttribLocation(prog,a);gl.enableVertexAttribArray(A[a])}
  for(const u of['uVP','uM','uScroll','uTex','uTint','uEm','uCam','uFogC','uFogD','uSky','uZen','uSunD','uSunK'])U[u]=gl.getUniformLocation(prog,u);
  U.LP=[0,1,2,3].map(i=>gl.getUniformLocation(prog,`uLP[${i}]`));U.LC=[0,1,2,3].map(i=>gl.getUniformLocation(prog,`uLC[${i}]`));
}
function mesh(arr){const b=gl.createBuffer();gl.bindBuffer(gl.ARRAY_BUFFER,b);gl.bufferData(gl.ARRAY_BUFFER,new Float32Array(arr),gl.STATIC_DRAW);return{b,n:arr.length/11}}
function bindMesh(m){gl.bindBuffer(gl.ARRAY_BUFFER,m.b);gl.vertexAttribPointer(A.aP,3,gl.FLOAT,false,44,0);gl.vertexAttribPointer(A.aN,3,gl.FLOAT,false,44,12);gl.vertexAttribPointer(A.aT,2,gl.FLOAT,false,44,24);gl.vertexAttribPointer(A.aC,3,gl.FLOAT,false,44,32)}

/* ---------- процедурные текстуры ---------- */
let seed=7;const rnd=()=>(seed=(seed*16807)%2147483647)/2147483647;
function grain(x,s,a){const d=x.getImageData(0,0,s,s),p=d.data;for(let i=0;i<p.length;i+=4){const n=(rnd()-.5)*a;p[i]+=n;p[i+1]+=n;p[i+2]+=n}x.putImageData(d,0,0)}
function shade(c,k){const v=hex(c).map(x=>Math.round(clamp(x*k,0,1)*255));return`rgb(${v})`}
const TX={};
function mkTex(name,draw,size=64){const c=document.createElement('canvas');c.width=c.height=size;const x=c.getContext('2d');draw(x,size);
  const t=gl.createTexture();gl.bindTexture(gl.TEXTURE_2D,t);gl.texImage2D(gl.TEXTURE_2D,0,gl.RGBA,gl.RGBA,gl.UNSIGNED_BYTE,c);gl.generateMipmap(gl.TEXTURE_2D);
  gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_WRAP_S,gl.REPEAT);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_WRAP_T,gl.REPEAT);
  gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MIN_FILTER,gl.LINEAR_MIPMAP_LINEAR);gl.texParameteri(gl.TEXTURE_2D,gl.TEXTURE_MAG_FILTER,gl.NEAREST);
  const an=gl.getExtension('EXT_texture_filter_anisotropic');if(an)gl.texParameterf(gl.TEXTURE_2D,an.TEXTURE_MAX_ANISOTROPY_EXT,4);TX[name]=t}
function makeTextures(){
  mkTex('white',(x,s)=>{x.fillStyle='#fff';x.fillRect(0,0,s,s);grain(x,s,18)},16);
  mkTex('floor',(x,s)=>{x.fillStyle='#4f463d';x.fillRect(0,0,s,s);for(let i=0;i<4;i++)for(let j=0;j<4;j++){x.fillStyle=shade('#4f463d',.85+rnd()*.3);x.fillRect(i*16+1,j*16+1,14,14)}
    x.fillStyle='#231d18';for(let i=0;i<4;i++){x.fillRect(i*16,0,1,s);x.fillRect(0,i*16,s,1)}grain(x,s,26);for(let k=0;k<5;k++){x.fillStyle='rgba(20,14,8,.25)';x.beginPath();x.arc(rnd()*s,rnd()*s,2+rnd()*5,0,7);x.fill()}});
  mkTex('stone',(x,s)=>{x.fillStyle='#3a3027';x.fillRect(0,0,s,s);for(let r=0;r<8;r++)for(let c=-1;c<4;c++){const o=r%2?8:0;x.fillStyle=shade('#6c5b49',.8+rnd()*.35);x.fillRect(c*16+o+1,r*8+1,14,6)}grain(x,s,22)});
  mkTex('wall',(x,s)=>{x.fillStyle='#5b3f2e';x.fillRect(0,0,s,s);for(let i=0;i<2;i++)for(let j=0;j<2;j++){const X=i*32,Y=j*32;x.fillStyle=shade('#5b3f2e',.9+rnd()*.2);x.fillRect(X+2,Y+2,28,28);
    x.fillStyle='#7c5a43';x.fillRect(X+2,Y+2,28,2);x.fillRect(X+2,Y+2,2,28);x.fillStyle='#2b1d15';x.fillRect(X+2,Y+28,28,2);x.fillRect(X+28,Y+2,2,28);
    x.fillStyle='#a07a5a';for(const[a,b]of[[5,5],[25,5],[5,25],[25,25]])x.fillRect(X+a,Y+b,2,2)}
    for(let k=0;k<14;k++){x.fillStyle=`rgba(${120+rnd()*60|0},${50+rnd()*30|0},20,.25)`;x.fillRect(rnd()*s,rnd()*s,1+rnd()*2,6+rnd()*18)}grain(x,s,20)});
  mkTex('metal',(x,s)=>{x.fillStyle='#4b535d';x.fillRect(0,0,s,s);for(let i=0;i<8;i++)for(let j=0;j<8;j++){x.fillStyle='#6f7884';x.fillRect(i*8+((j%2)*4)+1,j*8+2,4,1);x.fillStyle='#2d333a';x.fillRect(i*8+((j%2)*4)+1,j*8+3,4,1)}
    x.strokeStyle='#2a2f35';x.lineWidth=2;x.strokeRect(1,1,s-2,s-2);grain(x,s,14)});
  mkTex('crate',(x,s)=>{x.fillStyle='#7a5a2d';x.fillRect(0,0,s,s);for(let i=0;i<4;i++){x.fillStyle=shade('#7a5a2d',.85+rnd()*.25);x.fillRect(6,6+i*13,s-12,12)}
    x.strokeStyle='#45301a';x.lineWidth=6;x.strokeRect(3,3,s-6,s-6);x.lineWidth=5;x.beginPath();x.moveTo(6,6);x.lineTo(s-6,s-6);x.moveTo(s-6,6);x.lineTo(6,s-6);x.stroke();
    x.fillStyle='#b0a090';for(const[a,b]of[[3,3],[s-6,3],[3,s-6],[s-6,s-6]])x.fillRect(a,b,3,3);grain(x,s,20)});
  mkTex('trim',(x,s)=>{x.fillStyle='#1b1b1b';x.fillRect(0,0,s,s);x.fillStyle='#c9a227';for(let i=-4;i<8;i++){x.beginPath();x.moveTo(i*16,0);x.lineTo(i*16+8,0);x.lineTo(i*16+8+s,s);x.lineTo(i*16+s,s);x.fill()}grain(x,s,24)});
  mkTex('light',(x,s)=>{x.fillStyle='#3a3530';x.fillRect(0,0,s,s);const g=x.createLinearGradient(0,6,0,s-6);g.addColorStop(0,'#fff6d8');g.addColorStop(.5,'#ffe7a8');g.addColorStop(1,'#fff6d8');x.fillStyle=g;x.fillRect(6,6,s-12,s-12)});
  mkTex('pad',(x,s)=>{x.fillStyle='#16241c';x.fillRect(0,0,s,s);for(let r=26;r>4;r-=7){x.strokeStyle=r%2?'#59ff8a':'#1f8a45';x.lineWidth=3;x.beginPath();x.arc(s/2,s/2,r,0,7);x.stroke()}x.fillStyle='#aaffc4';x.beginPath();x.moveTo(32,14);x.lineTo(42,28);x.lineTo(22,28);x.fill()});
  mkTex('sand',(x,s)=>{x.fillStyle='#cdb68a';x.fillRect(0,0,s,s);for(let k=0;k<120;k++){x.fillStyle=rnd()<.5?'#b89f72':'#dcc79c';x.fillRect(rnd()*s,rnd()*s,1+rnd()*3,1+rnd()*2)}grain(x,s,22)});
  mkTex('paving',(x,s)=>{x.fillStyle='#8f8a82';x.fillRect(0,0,s,s);for(let r=0;r<4;r++)for(let c=-1;c<4;c++){const o=r%2?8:0;x.fillStyle=shade('#b8b0a2',.85+rnd()*.25);x.fillRect(c*16+o+1,r*16+1,14,14)}grain(x,s,18)});
  mkTex('plaster',(x,s)=>{x.fillStyle='#e6dcc6';x.fillRect(0,0,s,s);for(let k=0;k<30;k++){x.fillStyle=`rgba(150,130,100,${.05+rnd()*.08})`;x.beginPath();x.arc(rnd()*s,rnd()*s,2+rnd()*9,0,7);x.fill()}x.fillStyle='rgba(120,100,80,.25)';x.fillRect(0,s-3,s,3);grain(x,s,14)});
  mkTex('brick',(x,s)=>{x.fillStyle='#8a8075';x.fillRect(0,0,s,s);for(let r=0;r<8;r++)for(let c=-1;c<4;c++){const o=r%2?8:0;x.fillStyle=shade('#a24a32',.8+rnd()*.35);x.fillRect(c*16+o+1,r*8+1,14,6)}grain(x,s,20)});
  mkTex('roof',(x,s)=>{x.fillStyle='#7a3a22';x.fillRect(0,0,s,s);for(let r=0;r<8;r++)for(let c=0;c<8;c++){x.fillStyle=shade('#b8573a',.8+rnd()*.3);x.beginPath();x.arc(c*8+4+(r%2)*4,r*8+6,4,Math.PI,0);x.fill()}grain(x,s,16)});
  mkTex('wood',(x,s)=>{x.fillStyle='#8a6438';x.fillRect(0,0,s,s);for(let i=0;i<4;i++){x.fillStyle=shade('#9b7244',.8+rnd()*.3);x.fillRect(0,i*16+1,s,14);for(let k=0;k<6;k++){x.fillStyle='rgba(60,35,15,.3)';x.fillRect(rnd()*s,i*16+2+rnd()*12,6+rnd()*14,1)}}grain(x,s,14)});
  mkTex('water',(x,s)=>{x.fillStyle='#2f7fae';x.fillRect(0,0,s,s);for(let k=0;k<40;k++){x.strokeStyle=rnd()<.5?'rgba(160,215,245,.45)':'rgba(20,70,110,.4)';x.lineWidth=1+rnd()*1.5;const y=rnd()*s,X=rnd()*s;x.beginPath();x.moveTo(X,y);x.quadraticCurveTo(X+6,y-3,X+12,y);x.stroke()}});
  mkTex('cloth',(x,s)=>{for(let i=0;i<8;i++){x.fillStyle=i%2?'#f2ead8':'#c8402e';x.fillRect(i*8,0,8,s)}grain(x,s,14)});
  mkTex('leaves',(x,s)=>{x.fillStyle='#3d6b2a';x.fillRect(0,0,s,s);for(let k=0;k<160;k++){x.fillStyle=['#4f8a34','#2f5520','#66a040','#3a6a26'][k%4];x.beginPath();x.arc(rnd()*s,rnd()*s,1.5+rnd()*3,0,7);x.fill()}});
  mkTex('bark',(x,s)=>{x.fillStyle='#5a4028';x.fillRect(0,0,s,s);for(let k=0;k<20;k++){x.fillStyle='rgba(30,18,8,.4)';x.fillRect(rnd()*s,0,1+rnd()*2,s)}grain(x,s,16)});
  mkTex('lava',(x,s)=>{x.fillStyle='#b8260a';x.fillRect(0,0,s,s);for(let k=0;k<70;k++){const c=['#ff6a00','#ff9a1a','#e23a06','#ffd040','#8a1405'][k%5];x.fillStyle=c;x.globalAlpha=.5;x.beginPath();x.arc(rnd()*s,rnd()*s,2+rnd()*8,0,7);x.fill()}x.globalAlpha=1;grain(x,s,30)});
}
const MAT={sand:{t:'sand',s:4,tile:2.5},paving:{t:'paving',s:2},plaster:{t:'plaster',s:3},brick:{t:'brick',s:2},roof:{t:'roof',s:2},wood:{t:'wood',s:2},water:{t:'water',s:4,em:.2,scroll:1},cloth:{t:'cloth',box:1},leaves:{t:'leaves',s:2},bark:{t:'bark',s:1},
  floor:{t:'floor',s:4},stone:{t:'stone',s:2},wall:{t:'wall',s:4},metal:{t:'metal',s:2},crate:{t:'crate',box:1},trim:{t:'trim',s:1},light:{t:'light',box:1,em:1},pad:{t:'pad',box:1,em:.8},lava:{t:'lava',s:5,em:1,scroll:1}};

/* ---------- карта ---------- */
let boxes=[],mapMeshes=[],items=[],lights=[],WP=[];
let SUN=nrm([-.35,-1,-.25]),SUNC=[.5,.43,.36],AMB=[.3,.28,.28],TH={sky:[.16,.1,.07],zen:[.05,.03,.03],fog:.018,bound:32.4,floorMin:-4,name:'Арена'};
const MAPKEY=new URLSearchParams(location.search).get('map')==='town'?'town':'arena';
function rayBox(o,d,b,maxT){let tmin=0,tmax=maxT,ax=-1;for(let i=0;i<3;i++){if(Math.abs(d[i])<1e-9){if(o[i]<b[i]||o[i]>b[i+3])return null;continue}
  let t1=(b[i]-o[i])/d[i],t2=(b[i+3]-o[i])/d[i];if(t1>t2){const q=t1;t1=t2;t2=q}if(t1>tmin){tmin=t1;ax=i}if(t2<tmax)tmax=t2;if(tmin>tmax)return null}return[tmin,ax]}
function mapRay(o,d,maxT){let best=maxT,ax=-1,hit=false;for(const B of boxes){const r=rayBox(o,d,B.b,best);if(r&&r[0]<best){best=r[0];ax=r[1];hit=true}}return{t:best,ax,hit}}
function bake(p,n){const c=[AMB[0]+(n[1]>.5?.05:0),AMB[1]+(n[1]>.5?.05:0),AMB[2]+(n[1]>.5?.05:0)];const o=add(p,scl(n,.03));
  const sd=-dot(n,SUN);if(sd>0&&!mapRay(o,scl(SUN,-1),80).hit)for(let i=0;i<3;i++)c[i]+=SUNC[i]*sd;
  for(const L of lights){const d=sub(L.p,o),ds=len(d);if(ds>=L.r)continue;const dn=scl(d,1/ds),nd=dot(n,dn);if(nd<=-.2)continue;if(mapRay(o,dn,ds-.3).hit)continue;
    const a=(1-ds/L.r)**2*(.3+.7*Math.max(0,nd));for(let i=0;i<3;i++)c[i]+=L.c[i]*a*1.3}return c}
const FACES=[[[1,0,0],[0,1,0],[0,0,1]],[[-1,0,0],[0,0,1],[0,1,0]],[[0,1,0],[0,0,1],[1,0,0]],[[0,-1,0],[1,0,0],[0,0,1]],[[0,0,1],[1,0,0],[0,1,0]],[[0,0,-1],[0,1,0],[1,0,0]]];
function buildMap(){
  const per={};
  for(const B of boxes){const m=MAT[B.m]||MAT.wall,arr=per[B.m]||(per[B.m]=[]);const b=B.b,mn=[b[0],b[1],b[2]],mx=[b[3],b[4],b[5]],ctr=scl(add(mn,mx),.5),hs=scl(sub(mx,mn),.5);
    for(const[n,u,v]of FACES){const ax=n.findIndex(x=>x),fc=ctr.slice();fc[ax]+=n[ax]*hs[ax];
      if(Math.abs(fc[0])>TH.bound||Math.abs(fc[2])>TH.bound||fc[1]<TH.floorMin+.1)continue;if(n[1]<0&&b[1]<.1)continue;
      const tl=m.tile||1.5,ua=u.findIndex(x=>x),va=v.findIndex(x=>x),U2=hs[ua]*2,V2=hs[va]*2,nu=Math.max(1,Math.ceil(U2/tl)),nv=Math.max(1,Math.ceil(V2/tl));
      const vert=(i,j)=>{const p=fc.slice();p[ua]+=-hs[ua]+U2*i/nu;p[va]+=-hs[va]+V2*j/nv;let s,t;
        if(m.box){s=(p[ua]-mn[ua])/U2;t=(p[va]-mn[va])/V2;if(ua===1){const q=s;s=t;t=q}}
        else{const ux=ax===0?2:0,vy=ax===1?2:1;s=p[ux]/m.s;t=-p[vy]/m.s}return{p,s,t,c:m.em?[1,1,1]:bake(p,n)}};
      const g=[];for(let i=0;i<=nu;i++){g[i]=[];for(let j=0;j<=nv;j++)g[i][j]=vert(i,j)}
      const pv=q=>arr.push(q.p[0],q.p[1],q.p[2],n[0],n[1],n[2],q.s,q.t,q.c[0],q.c[1],q.c[2]);
      for(let i=0;i<nu;i++)for(let j=0;j<nv;j++){const a=g[i][j],b2=g[i+1][j],c=g[i+1][j+1],d=g[i][j+1];pv(a);pv(b2);pv(c);pv(a);pv(c);pv(d)}}}
  mapMeshes=Object.entries(per).map(([k,arr])=>Object.assign(mesh(arr),{m:MAT[k]||MAT.wall}));
}
let CUBE;
function makeCube(){const a=[];const sh=[1,.82,1.05,.55,.9,.72];FACES.forEach(([n,u,v],fi)=>{const ax=n.findIndex(x=>x);const c=[0,0,0];c[ax]=n[ax]*.5;const k=sh[fi];
  const P=(i,j)=>{const p=c.slice();const ua=u.findIndex(x=>x),va=v.findIndex(x=>x);p[ua]=i-.5;p[va]=j-.5;a.push(p[0],p[1],p[2],n[0],n[1],n[2],i,j,k,k,k)};
  P(0,0);P(1,0);P(1,1);P(0,0);P(1,1);P(0,1)});CUBE=mesh(a)}

/* ---------- модели ---------- */
const COLORS=['#e04b3a','#3a7be0','#45c05a','#e0c23a','#a64be0','#e07f3a','#3ad0d0','#e04ba6'].map(hex);
const GUNS=[
 [[0,-.06,.03,.05,.1,.06,'#333'],[0,0,-.08,.07,.07,.24,'#6b6b70'],[0,0,-.21,.045,.045,.03,'#ff8a20',1]],
 [[0,-.02,.13,.06,.08,.2,'#6b4423'],[0,0,-.05,.08,.08,.2,'#3a3a3e'],[-.022,.01,-.33,.035,.035,.4,'#1d1d1d'],[.022,.01,-.33,.035,.035,.4,'#1d1d1d']],
 [[0,0,-.05,.09,.1,.3,'#4a4f55'],[0,.01,-.33,.03,.03,.3,'#1e1e1e'],[0,-.1,-.03,.05,.13,.06,'#2a2a2a'],[0,.065,-.06,.02,.03,.12,'#111']],
 [[0,0,-.02,.1,.1,.26,'#3d4a3a'],[0,-.035,0,.13,.13,.1,'#5f7040'],[0,.01,-.24,.07,.07,.2,'#2c2c2c']],
 [[0,.02,-.1,.11,.11,.62,'#55603a'],[0,.02,-.43,.08,.08,.04,'#d0381e',.6],[0,-.08,.03,.05,.1,.06,'#222'],[.07,.06,-.05,.03,.05,.12,'#333']],
 [[0,0,-.08,.1,.09,.32,'#3a4250'],[0,0,-.12,.12,.06,.04,'#40e0ff',1],[0,0,-.2,.12,.06,.04,'#40e0ff',1],[0,0,-.3,.04,.04,.12,'#222']],
 [[0,0,-.1,.08,.1,.52,'#2f3440'],[0,.06,-.15,.03,.02,.42,'#4aa8ff',1],[0,-.06,-.15,.03,.02,.42,'#4aa8ff',1],[0,-.02,.19,.06,.09,.14,'#222']],

 [[0,0,-.05,.08,.08,.36,'#5a5f66'],[0,-.09,.02,.11,.13,.16,'#d06a20'],[0,.01,-.27,.06,.06,.06,'#2a2a2a'],[0,.01,-.31,.03,.03,.03,'#40a0ff',1]],
 [[0,0,-.05,.18,.16,.42,'#2b3a2e'],[0,0,-.12,.2,.12,.12,'#5cff6a',1],[0,0,-.3,.12,.12,.08,'#1a1a1a'],[0,.11,0,.06,.05,.24,'#444']],
].map(g=>g.map(q=>({o:q.slice(0,3),s:q.slice(3,6),c:hex(q[6]),e:q[7]||0})));
const MUZZLE=[-.23,-.53,-.48,-.36,-.46,-.37,-.4,-.33,-.36];

/* ---------- рисование ---------- */
let curTex=null;
function tex(t){if(t!==curTex){gl.bindTexture(gl.TEXTURE_2D,t);curTex=t}}
let DIM=1;
function cube(m,c,em=0,al=1,t=TX.white){gl.uniformMatrix4fv(U.uM,false,m);const k=em?1:DIM;gl.uniform4f(U.uTint,c[0]*k,c[1]*k,c[2]*k,al);gl.uniform1f(U.uEm,em);tex(t);gl.drawArrays(gl.TRIANGLES,0,36)}
function part(base,q,k=1){const m=new Float32Array(base);T(m,q.o[0]*k,q.o[1]*k,q.o[2]*k);SC(m,q.s[0]*k,q.s[1]*k,q.s[2]*k);cube(m,q.c,q.e)}
function drawGun(base,w,k){for(const q of GUNS[w]||GUNS[0])part(base,q,k)}
function orient(m,d){const h=Math.hypot(d[0],d[2]);RY(m,Math.atan2(-d[0],-d[2]));RX(m,Math.atan2(d[1],h));return m}
function drawPlayer(o,t){
  const c=COLORS[o.c%8],dk=[.16,.16,.18],m0=T(M4(),o.p[0],o.p[1],o.p[2]);RY(m0,o.yaw);
  const sw=Math.sin(t*10)*Math.min(1,o.spd/6)*.6;
  for(const s of[-1,1]){const m=new Float32Array(m0);T(m,.14*s,.82,0);RX(m,sw*s);T(m,0,-.41,0);SC(m,.22,.82,.26);cube(m,dk)}
  let m=new Float32Array(m0);T(m,0,1.17,0);SC(m,.62,.68,.34);cube(m,c);
  m=new Float32Array(m0);T(m,0,1.2,-.18);SC(m,.4,.3,.04);cube(m,scl(c,.55));
  m=new Float32Array(m0);T(m,0,1.68,0);RX(m,o.pitch*.5);SC(m,.36,.36,.38);cube(m,[.25,.25,.28]);
  m=new Float32Array(m0);T(m,0,1.68,0);RX(m,o.pitch*.5);T(m,0,.02,-.19);SC(m,.3,.1,.02);cube(m,c,1);
  m=new Float32Array(m0);T(m,.27,1.32,-.05);RX(m,o.pitch);T(m,0,0,-.12);drawGun(m,o.w,1.3);
}

/* ---------- состояние ---------- */
const P={p:[0,0,0],v:[0,0,0],yaw:0,pitch:0,ground:false,was:false,gb:null,alive:false};
const ME={hp:100,ar:0,am:{},has:[0,2],f:0,d:0,w:2,prevW:0};
let myId=0,myName='',myColor=0,ws=null,connected=false,rtt=80,snaps=[],scores=[],feed=[],msgs=[],fx=[],parts=[];
let deathMsg='',dmgFlash=0,dmgDir=null,hitMark=0,shake=0,recoil=0,switchT=0,nextFire=0,firing=false,lastSend=0,dead0=0;
const HW=.4,HH=1.8,EYE=1.6,STEP=.56;
function hits(p){for(const B of boxes){const b=B.b;if(p[0]+HW>b[0]+1e-4&&p[0]-HW<b[3]-1e-4&&p[1]+HH>b[1]+1e-4&&p[1]<b[4]-1e-4&&p[2]+HW>b[2]+1e-4&&p[2]-HW<b[5]-1e-4)return B}return null}
function moveAxis(ax,d){if(!d)return;const p=P.p;p[ax]+=d;for(let k=0;k<3;k++){const B=hits(p);if(!B)return;const b=B.b;
  if(ax===1){if(d<0){p[1]=b[4];P.v[1]=0;P.ground=true;P.gb=B}else{p[1]=b[1]-HH-1e-3;P.v[1]=Math.min(0,P.v[1])}continue}
  if(P.was&&b[4]-p[1]>0&&b[4]-p[1]<=STEP){const oy=p[1];p[1]=b[4]+1e-3;if(!hits(p))return;p[1]=oy}
  p[ax]=d>0?b[ax]-HW-1e-3:b[ax+3]+HW+1e-3;P.v[ax]=0}}
const K={};let jumpQ=false,mvx=0,mvy=0;
function physics(dt){
  const f=[-Math.sin(P.yaw),0,-Math.cos(P.yaw)],r=[Math.cos(P.yaw),0,-Math.sin(P.yaw)];
  let fw=(K.KeyW||K.ArrowUp?1:0)-(K.KeyS||K.ArrowDown?1:0)-mvy,st=(K.KeyD||K.ArrowRight?1:0)-(K.KeyA||K.ArrowLeft?1:0)+mvx;
  let w=add(scl(f,fw),scl(r,st));const wl=Math.hypot(w[0],w[2]);if(wl>1)w=scl(w,1/wl);
  const SP=8.2,v=P.v;
  if(P.ground){const k=Math.min(1,11*dt);v[0]+=(w[0]*SP-v[0])*k;v[2]+=(w[2]*SP-v[2])*k}
  else{const hs=Math.hypot(v[0],v[2]);v[0]+=w[0]*16*dt;v[2]+=w[2]*16*dt;const n=Math.hypot(v[0],v[2]),cap=Math.max(SP,hs);if(n>cap){v[0]*=cap/n;v[2]*=cap/n}}
  if((K.Space||jumpQ)&&P.ground){v[1]=7.4;P.ground=false;snd('jump')}jumpQ=false;
  v[1]-=20*dt;
  P.was=P.ground;P.ground=false;
  const n=Math.max(1,Math.ceil(Math.max(Math.abs(v[0]),Math.abs(v[1]),Math.abs(v[2]))*dt/.2)),h=dt/n;
  for(let i=0;i<n;i++){moveAxis(0,v[0]*h);moveAxis(2,v[2]*h);moveAxis(1,v[1]*h)}
  if(P.ground&&P.gb&&P.gb.p){v[0]=P.gb.p[0];v[1]=P.gb.p[1];v[2]=P.gb.p[2];P.ground=false;snd('pad')}
  P.spd=Math.hypot(v[0],v[2]);
}

/* ---------- сеть ---------- */
function connect(){
  $('#mst').textContent='Подключение к серверу…';
  ws=new WebSocket((location.protocol==='https:'?'wss://':'ws://')+location.host+'/api/game/ws?map='+MAPKEY+'&token='+encodeURIComponent(SES.token));
  ws.onopen=()=>{connected=true};
  ws.onclose=()=>{connected=false;$('#mst').textContent='Связь потеряна. Переподключение…';showMenu();setTimeout(connect,2000)};
  ws.onmessage=e=>{let m;try{m=JSON.parse(e.data)}catch(_){return}onMsg(m)};
}
const sendWS=o=>{if(ws&&ws.readyState===1)ws.send(JSON.stringify(o))};
let ready=false;
function onMsg(m){
  switch(m.t){
  case'hi':myId=m.id;myName=m.name;myColor=m.c;WP=m.wp;lights=m.map.lights;
    if(m.map.theme){const t=m.map.theme;TH=t;SUN=nrm(t.sun);SUNC=t.sunc;AMB=t.amb;document.title=t.name;$('#mname').textContent='Карта: '+t.name}
    if(!ready){boxes=m.map.boxes;seed=7;const t0=performance.now();buildMap();console.log('map baked',Math.round(performance.now()-t0),'ms');ready=true}
    items=m.items.map(i=>({k:i.k,p:i.p,on:i.on}));$('#mst').textContent='Готово: '+myName;$('#play').disabled=false;break;
  case'me':ME.hp=m.hp;ME.ar=m.ar;ME.am=m.am||{};ME.has=m.has;ME.f=m.f;ME.d=m.d;if(!m.alive&&P.alive){P.alive=false;dead0=performance.now()}if(!ME.has.includes(ME.w))ME.w=bestWeapon();break;
  case's':snaps.push({lt:performance.now(),pl:m.pl,pr:m.pr});if(snaps.length>40)snaps.shift();break;
  case'sc':scores=m.l;break;
  case'po':rtt=performance.now()-m.c;break;
  case'ev':onEv(m);break;
  }
}
function onEv(m){
  const now=performance.now();
  switch(m.k){
  case'spawn':P.p=m.p.slice();P.v=[0,0,0];P.yaw=m.yaw;P.pitch=0;P.alive=true;deathMsg='';ME.w=2;break;
  case'shot':if(m.id===myId)break;{const w=WP[m.w];if(w.k==='rail')for(const e of m.e)railFx(m.o,e);else if(w.k==='hit')for(const e of m.e)tracer(m.o,e,m.w===0?[1,.6,.2]:[1,.85,.5]);
    snd('w'+m.w,m.o);fx.push({k:'flash',p:m.o,t0:now,dur:60})}break;
  case'boom':boomFx(m.p,m.r,m.w);break;
  case'spark':for(let i=0;i<8;i++)parts.push({p:m.p.slice(),v:[(Math.random()-.5)*6,Math.random()*4,(Math.random()-.5)*6],l:.35,L:.35,s:.06,c:[.3,.9,1],add:1});snd('spark',m.p);break;
  case'hurt':dmgFlash=Math.min(1,dmgFlash+m.d/60);dmgDir=m.f;snd('pain');break;
  case'hm':hitMark=now;snd('hit');break;
  case'kb':P.v=add(P.v,m.v);P.ground=false;break;
  case'kill':feed.push({a:m.a,v:m.v,w:m.w,t:now});if(feed.length>6)feed.shift();
    if(m.vid===myId){deathMsg=m.a?'Вас убил '+m.a:'Вы погибли';snd('die')}else if(m.a===myName)msg('Вы убили '+m.v);break;
  case'item':if(items[m.i])items[m.i].on=m.on;break;
  case'pick':msg(m.s);snd(m.w!=null?'wpick':'pick');if(m.w!=null&&m.new&&!firing&&m.w>ME.w)setWeapon(m.w);break;
  case'join':feed.push({sys:m.n+' зашёл в игру',t:now});break;
  case'leave':feed.push({sys:m.n+' вышел',t:now});break;
  }
}
function msg(t){msgs.push({t,at:performance.now()});if(msgs.length>3)msgs.shift()}

/* ---------- оружие ---------- */
const ammoOf=w=>{const a=WP[w]&&WP[w].a;return a?(ME.am[a]||0):Infinity};
function bestWeapon(){for(const w of[8,4,6,5,7,2,1,3,0])if(ME.has.includes(w)&&ammoOf(w)>0)return w;return 0}
function setWeapon(w){if(w===ME.w||!ME.has.includes(w))return;ME.prevW=ME.w;ME.w=w;switchT=performance.now();snd('switch')}
function cycle(dir){const own=[0,1,2,3,4,5,6,7,8].filter(w=>ME.has.includes(w));let i=own.indexOf(ME.w);i=(i+dir+own.length)%own.length;setWeapon(own[i])}
function eye(){return[P.p[0],P.p[1]+EYE,P.p[2]]}
function tryFire(now){
  if(!firing||!P.alive||now<nextFire||now-switchT<220)return;const w=ME.w,wp=WP[w];if(!wp)return;
  if(ammoOf(w)<=0){nextFire=now+400;snd('empty');const b=bestWeapon();if(b!==w)setWeapon(b);return}
  nextFire=now+wp.r;if(wp.a)ME.am[wp.a]--;
  const o=eye(),d=fwdOf(P.yaw,P.pitch);sendWS({t:'fire',w,o,d,lag:100+rtt/2});
  recoil=1;snd('w'+w);fx.push({k:'flash',p:add(o,scl(d,.6)),t0:now,dur:60,me:1});
  if(wp.k==='hit'){const n=w===1?6:1,sp=w===1?.075:w===2?.02:0;for(let i=0;i<n;i++){const dd=nrm(add(d,[(Math.random()-.5)*2*sp,(Math.random()-.5)*2*sp,(Math.random()-.5)*2*sp]));const r=mapRay(o,dd,200);const e=add(o,scl(dd,r.t));
    const g=add(add(o,scl(d,.5)),[Math.cos(P.yaw)*.18,-.2,-Math.sin(P.yaw)*.18]);tracer(g,e,w===0?[1,.6,.2]:[1,.85,.5]);if(r.hit)impact(e,r.ax,dd)}}
  else if(wp.k==='rail'){const r=mapRay(o,d,200);railFx(add(add(o,scl(d,.5)),[Math.cos(P.yaw)*.18,-.2,-Math.sin(P.yaw)*.18]),add(o,scl(d,r.t)))}
}

/* ---------- эффекты ---------- */
function tracer(a,b,c){fx.push({k:'tr',a,b,c,t0:performance.now(),dur:70})}
function railFx(a,b){const now=performance.now();fx.push({k:'rail',a,b,t0:now,dur:900});const d=sub(b,a),L=len(d),n=nrm(d);
  for(let s=0;s<L;s+=.5){const q=add(a,scl(n,s)),an=s*2.2;parts.push({p:add(q,[Math.cos(an)*.12,Math.sin(an)*.12,0]),v:[Math.cos(an)*.5,Math.sin(an)*.5,(Math.random()-.5)*.4],l:.7,L:.7,s:.05,c:[.4,.7,1],add:1,g:0})}snd('w6',a)}
function impact(p,ax,d){for(let i=0;i<4;i++){const v=[(Math.random()-.5)*3,Math.random()*3,(Math.random()-.5)*3];if(ax>=0)v[ax]=-Math.sign(d[ax])*Math.random()*4;parts.push({p:p.slice(),v,l:.3,L:.3,s:.04,c:[1,.8,.4],add:1})}}
function boomFx(p,r,w){const now=performance.now();fx.push({k:'boom',p,r,w,t0:now,dur:w===8?900:550});snd('boom',p);const dd=len(sub(p,eye()));shake=Math.max(shake,clamp(1-dd/18,0,1));
  for(let i=0;i<22;i++)parts.push({p:p.slice(),v:[(Math.random()-.5)*14,Math.random()*10,(Math.random()-.5)*14],l:.6+Math.random()*.4,L:1,s:.07+Math.random()*.08,c:[1,.5+Math.random()*.4,.15],add:1});
  for(let i=0;i<10;i++)parts.push({p:p.slice(),v:[(Math.random()-.5)*3,1+Math.random()*2,(Math.random()-.5)*3],l:1.2,L:1.2,s:.35,c:[.25,.22,.2],add:0,g:-1})}

/* ---------- звук ---------- */
let AC=null,MS=null,NB=null;
function initAudio(){if(AC)return;try{AC=new(window.AudioContext||window.webkitAudioContext)();MS=AC.createGain();MS.gain.value=.45;MS.connect(AC.destination);
  NB=AC.createBuffer(1,AC.sampleRate,AC.sampleRate);const d=NB.getChannelData(0);for(let i=0;i<d.length;i++)d[i]=Math.random()*2-1}catch(e){AC=null}}
function tone(f0,f1,dur,type,vol,dl=0){const t=AC.currentTime+dl,o=AC.createOscillator(),g=AC.createGain();o.type=type;o.frequency.setValueAtTime(f0,t);o.frequency.exponentialRampToValueAtTime(Math.max(20,f1),t+dur);
  g.gain.setValueAtTime(vol,t);g.gain.exponentialRampToValueAtTime(.001,t+dur);o.connect(g);g.connect(MS);o.start(t);o.stop(t+dur+.02)}
function noise(dur,ft,f0,f1,vol,q=1){const t=AC.currentTime,s=AC.createBufferSource(),fl=AC.createBiquadFilter(),g=AC.createGain();s.buffer=NB;fl.type=ft;fl.Q.value=q;fl.frequency.setValueAtTime(f0,t);fl.frequency.exponentialRampToValueAtTime(Math.max(30,f1),t+dur);
  g.gain.setValueAtTime(vol,t);g.gain.exponentialRampToValueAtTime(.001,t+dur);s.connect(fl);fl.connect(g);g.connect(MS);s.start(t,Math.random()*.5);s.stop(t+dur+.02)}
function snd(k,pos){if(!AC)return;let v=1;if(pos){v=clamp(1-len(sub(pos,eye()))/45,0,1);if(v<.03)return}const V=x=>x*v;
  switch(k){
  case'w0':tone(950,260,.13,'square',V(.18));break;
  case'w1':noise(.35,'lowpass',2200,300,V(.9));tone(130,45,.22,'sine',V(.6));break;
  case'w2':noise(.07,'bandpass',2600,1200,V(.55),2);tone(190,80,.05,'square',V(.12));break;
  case'w3':tone(320,140,.16,'triangle',V(.4));noise(.12,'lowpass',900,200,V(.4));break;
  case'w4':noise(.55,'lowpass',500,2400,V(.55));tone(160,55,.3,'sawtooth',V(.25));break;
  case'w5':tone(1500,900,.07,'sawtooth',V(.14));break;
  case'w6':tone(1900,160,.65,'sine',V(.32));noise(.3,'highpass',3500,2000,V(.28));break;
  case'w7':noise(.12,'bandpass',900,600,V(.35),.8);break;
  case'w8':tone(120,900,.5,'sawtooth',V(.28));tone(60,40,.6,'sine',V(.5));break;
  case'boom':noise(1,'lowpass',700,60,V(1.1));tone(90,28,.7,'sine',V(.8));break;
  case'spark':tone(2200,800,.06,'square',V(.08));break;
  case'jump':tone(260,420,.08,'sine',.08);break;
  case'pad':tone(200,950,.38,'triangle',.25);noise(.3,'bandpass',600,2500,.15);break;
  case'hit':tone(1300,1250,.05,'square',.1);break;
  case'pain':tone(420,200,.18,'sawtooth',.18);break;
  case'die':tone(320,55,.7,'sawtooth',.25);break;
  case'pick':tone(700,1400,.12,'sine',.22);tone(1050,2000,.1,'sine',.15,.08);break;
  case'wpick':tone(400,800,.15,'square',.12);tone(800,1600,.15,'square',.1,.1);break;
  case'switch':tone(600,500,.04,'square',.05);break;
  case'empty':tone(2400,2300,.02,'square',.08);break;
  }}

/* ---------- кадр ---------- */
let W=1,H=1,DPR=1,PROJ,VMPROJ,last=performance.now(),fps=60,fpsA=0,fpsN=0,fpsT=0;
function resize(){DPR=coarse?Math.min(devicePixelRatio,1.25):Math.min(devicePixelRatio,1.5);W=innerWidth;H=innerHeight;cv.width=W*DPR|0;cv.height=H*DPR|0;const hd=Math.min(devicePixelRatio,2);hud.width=W*hd|0;hud.height=H*hd|0;hx.setTransform(hd,0,0,hd,0,0);
  PROJ=persp(1.45/Math.max(1,(W/H)/1.6)**.35,W/H,.05,220);VMPROJ=persp(1.1,W/H,.01,10)}
addEventListener('resize',resize);
function interp(now){
  const rt=now-110;let a=null,b=null;for(let i=snaps.length-1;i>=0;i--){if(snaps[i].lt<=rt){a=snaps[i];b=snaps[i+1]||null;break}}
  if(!a){a=snaps[0];b=null}if(!a)return{pl:[],pr:[]};const k=b?clamp((rt-a.lt)/(b.lt-a.lt),0,1):0;
  const bm=new Map();if(b)for(const q of b.pl)bm.set(q[0],q);const pl=[];
  for(const q of a.pl){const r=bm.get(q[0])||q;const p=[lerp(q[1],r[1],k),lerp(q[2],r[2],k),lerp(q[3],r[3],k)];const prev=interp.pos.get(q[0]);let spd=prev?Math.hypot(p[0]-prev[0],p[2]-prev[2])/Math.max(.001,(now-interp.t)/1000):0;interp.pos.set(q[0],p);
    pl.push({id:q[0],p,yaw:angLerp(q[4],r[4],k),pitch:lerp(q[5],r[5],k),w:r[6],alive:r[7],c:r[8],spd:Math.min(spd,12)})}
  interp.t=now;
  const pm=new Map();if(b)for(const q of b.pr)pm.set(q[0],q);const pr=[];
  for(const q of a.pr){const r=pm.get(q[0]);if(!r)continue;pr.push({id:q[0],w:q[1],p:[lerp(q[2],r[2],k),lerp(q[3],r[3],k),lerp(q[4],r[4],k)],d:nrm(sub([r[2],r[3],r[4]],[q[2],q[3],q[4]]))})}
  return{pl,pr}}
interp.pos=new Map();interp.t=0;
function frame(now){
  requestAnimationFrame(frame);const dt=Math.min(.1,(now-last)/1000);last=now;fpsA+=dt;fpsN++;if(now-fpsT>500){fps=Math.round(fpsN/fpsA);fpsA=0;fpsN=0;fpsT=now}
  if(!ready){return}
  if(P.alive){aimAssist(dt);physics(dt);tryFire(now);if(now-lastSend>50){lastSend=now;sendWS({t:'st',p:P.p.map(x=>Math.round(x*100)/100),y:Math.round(P.yaw*1000)/1000,x:Math.round(P.pitch*1000)/1000,w:ME.w})}}
  if(now-(frame.ping||0)>2000){frame.ping=now;sendWS({t:'pi',c:now})}
  render(now,dt);drawHUD(now);
}
function setLights(list){for(let i=0;i<4;i++){const L=list[i];if(L){gl.uniform4f(U.LP[i],L.p[0],L.p[1],L.p[2],L.r);gl.uniform3f(U.LC[i],L.c[0],L.c[1],L.c[2])}else gl.uniform4f(U.LP[i],0,0,0,0)}}
function render(now,dt){
  gl.viewport(0,0,cv.width,cv.height);const fog=TH.sky;gl.clearColor(fog[0],fog[1],fog[2],1);gl.clear(gl.COLOR_BUFFER_BIT|gl.DEPTH_BUFFER_BIT);
  gl.enable(gl.DEPTH_TEST);gl.enable(gl.CULL_FACE);gl.disable(gl.BLEND);gl.useProgram(prog);curTex=null;
  let e=eye(),yaw=P.yaw,pitch=P.pitch;
  if(!P.alive){const k=clamp((now-dead0)/600,0,1);e=[P.p[0],P.p[1]+lerp(EYE,.35,k),P.p[2]];pitch=lerp(P.pitch,-.2,k)}
  shake=Math.max(0,shake-dt*2.5);const sh=shake*.08;if(sh){e=add(e,[(Math.random()-.5)*sh,(Math.random()-.5)*sh,(Math.random()-.5)*sh])}
  const bob=P.alive&&P.was?Math.sin(now/95)*Math.min(1,(P.spd||0)/8):0;e[1]+=bob*.04;
  const V=M4();RZ(V,!P.alive?Math.min(.4,(now-dead0)/1500):0);RX(V,-pitch);RY(V,-yaw);T(V,-e[0],-e[1],-e[2]);
  gl.uniformMatrix4fv(U.uVP,false,mul(PROJ,V));gl.uniform3f(U.uCam,e[0],e[1],e[2]);gl.uniform3f(U.uFogC,fog[0],fog[1],fog[2]);gl.uniform1f(U.uFogD,TH.fog);gl.uniform2f(U.uScroll,0,0);
  gl.uniform3f(U.uZen,...TH.zen);gl.uniform3f(U.uSunD,...SUN);gl.uniform1f(U.uSunK,MAPKEY==='town'?1:0);
  bindMesh(CUBE);gl.disable(gl.CULL_FACE);gl.depthMask(false);gl.uniform1f(U.uSky,1);cube(SC(T(M4(),e[0],e[1],e[2]),200,200,200),[1,1,1]);gl.uniform1f(U.uSky,0);gl.depthMask(true);gl.enable(gl.CULL_FACE);
  const S=interp(now);
  // динамический свет
  const dl=[];for(const f of fx){const k=(now-f.t0)/f.dur;if(k>1)continue;if(f.k==='boom')dl.push({p:f.p,r:f.r*3.2*(1-k*.6),c:f.w===8?[.5*(1-k),1.8*(1-k),.6*(1-k)]:[1.6*(1-k),.9*(1-k),.3*(1-k)]});else if(f.k==='flash')dl.push({p:f.p,r:7,c:[1.2,.8,.4]})}
  let fl=0;for(const q of S.pr){if(q.w===4)dl.push({p:q.p,r:6,c:[1.2,.6,.2]});else if(q.w===5)dl.push({p:q.p,r:4,c:[.3,.9,1.3]});else if(q.w===8)dl.push({p:q.p,r:10,c:[.4,1.6,.5]});else if(q.w===7&&fl++%4===0)dl.push({p:q.p,r:5,c:[1.4,.7,.2]})}
  dl.sort((a,b)=>len(sub(a.p,e))-len(sub(b.p,e)));setLights(dl.slice(0,4));
  // карта
  for(const ms of mapMeshes){bindMesh(ms);tex(TX[ms.m.t]);gl.uniformMatrix4fv(U.uM,false,M4());gl.uniform4f(U.uTint,1,1,1,1);gl.uniform1f(U.uEm,ms.m.em||0);
    gl.uniform2f(U.uScroll,ms.m.scroll?now/9000:0,ms.m.scroll?now/14000:0);gl.drawArrays(gl.TRIANGLES,0,ms.n)}
  gl.uniform2f(U.uScroll,0,0);bindMesh(CUBE);
  // предметы
  for(const it of items){if(!it.on)continue;const m=T(M4(),it.p[0],it.p[1]+.55+Math.sin(now/400+it.p[0])*.12,it.p[2]);RY(m,now/700);
    if(it.k[0]==='w')drawGun(m,+it.k[1]-1,2.4);
    else if(it.k==='hp'){cube(SC(new Float32Array(m),.5,.5,.5),[.92,.92,.9]);cube(SC(new Float32Array(m),.52,.14,.38),[.9,.1,.1],.4);cube(SC(new Float32Array(m),.14,.52,.38),[.9,.1,.1],.4);cube(SC(new Float32Array(m),.38,.14,.52),[.9,.1,.1],.4)}
    else{cube(SC(new Float32Array(m),.62,.5,.3),[.25,.55,.25]);cube(SC(T(new Float32Array(m),0,.12,0),.64,.08,.32),[1,.85,.2],1)}
    const g=T(M4(),it.p[0],it.p[1]+.03,it.p[2]);cube(SC(g,.8,.02,.8),it.k[0]==='w'?[.6,.36,.12]:it.k==='hp'?[.6,.18,.18]:[.25,.6,.25],1)}
  // игроки
  for(const o of S.pl){if(o.id===myId||!o.alive)continue;drawPlayer(o,now/1000)}
  // снаряды
  const addq=[];for(const q of S.pr){const m=T(M4(),q.p[0],q.p[1],q.p[2]);
    if(q.w===4){orient(m,q.d);cube(SC(new Float32Array(m),.12,.12,.45),[.3,.33,.2]);cube(SC(T(new Float32Array(m),0,0,.26),.1,.1,.1),[1,.6,.2],1);
      if(Math.random()<.6)parts.push({p:q.p.slice(),v:[(Math.random()-.5)*.6,.4,(Math.random()-.5)*.6],l:.7,L:.7,s:.18,c:[.35,.33,.3],add:0,g:-1})}
    else if(q.w===3){RY(m,now/90);RX(m,now/120);cube(SC(m,.2,.2,.2),[.3,.38,.22])}
    else if(q.w===7){addq.push({p:q.p,s:.3+Math.random()*.35,c:[1,.45+Math.random()*.35,.1],a:.75});if(Math.random()<.15)parts.push({p:q.p.slice(),v:[0,1.5,0],l:.6,L:.6,s:.25,c:[.3,.28,.26],add:0,g:-1})}
    else if(q.w===8){RY(m,now/200);RX(m,now/260);cube(SC(m,.55,.55,.55),[.5,1,.55],1);addq.push({p:q.p,s:1.3+Math.sin(now/60)*.2,c:[.3,1,.4],a:.35})}
    else cube(SC(RY(m,now/50),.22,.22,.22),[.4,.95,1.2],1)}
  // частицы
  const pd=[];parts=parts.filter(p=>(p.l-=dt)>0);if(parts.length>500)parts.splice(0,parts.length-500);
  for(const p of parts){p.v[1]-=(p.g==null?9:p.g)*dt;p.p=add(p.p,scl(p.v,dt));if(!p.add){const k=p.l/p.L;const m=T(M4(),p.p[0],p.p[1],p.p[2]);RY(m,p.l*3);const s=p.s*(2-k);cube(SC(m,s,s,s),p.c)}else pd.push(p)}
  // яркие эффекты
  gl.enable(gl.BLEND);gl.blendFunc(gl.SRC_ALPHA,gl.ONE);gl.depthMask(false);gl.disable(gl.CULL_FACE);
  for(const p of pd){const k=p.l/p.L;const m=T(M4(),p.p[0],p.p[1],p.p[2]);cube(SC(m,p.s,p.s,p.s),p.c,1,k)}
  for(const q of addq){const m=T(M4(),...q.p);RY(m,Math.random()*3);RX(m,Math.random()*3);cube(SC(m,q.s,q.s,q.s),q.c,1,q.a)}
  fx=fx.filter(f=>now-f.t0<f.dur);
  for(const f of fx){const k=(now-f.t0)/f.dur;
    if(f.k==='tr'){const d=sub(f.b,f.a),L=len(d);const m=T(M4(),...add(f.a,scl(d,.5)));orient(m,d);cube(SC(m,.025,.025,L),f.c,1,.8*(1-k))}
    else if(f.k==='rail'){const d=sub(f.b,f.a),L=len(d);const m=T(M4(),...add(f.a,scl(d,.5)));orient(m,d);cube(SC(new Float32Array(m),.04,.04,L),[.85,.95,1],1,1-k);cube(SC(m,.14,.14,L),[.2,.45,1],1,.5*(1-k))}
    else if(f.k==='boom'){const G=f.w===8;for(let i=0;i<3;i++){const m=T(M4(),...f.p);RY(m,i*1.1+k);RX(m,i*.7);const s=f.r*(.25+.75*Math.sqrt(k))*(i?0.8:1);cube(SC(m,s,s,s),G?(i?[.2,1,.3]:[.7,1,.7]):(i?[1,.35,.08]:[1,.75,.3]),1,(1-k)*.55)}}
    else if(f.k==='flash'&&!f.me){const m=T(M4(),...f.p);RY(m,Math.random()*3);cube(SC(m,.3,.3,.3),[1,.7,.3],1,1-k)}}
  gl.depthMask(true);gl.disable(gl.BLEND);gl.enable(gl.CULL_FACE);
  // оружие в руках
  if(P.alive){gl.clear(gl.DEPTH_BUFFER_BIT);gl.uniformMatrix4fv(U.uVP,false,VMPROJ);gl.uniform1f(U.uFogD,0);gl.uniform3f(U.uCam,0,0,0);setLights([]);
    recoil=Math.max(0,recoil-dt*7);const sk=clamp((now-switchT)/220,0,1);const bx=Math.sin(now/190)*Math.min(1,(P.spd||0)/8)*.012;
    DIM=.62;const m=T(M4(),.16+bx,-.16-(1-sk)*.22+Math.abs(bx)*.6,-.3+recoil*.06);RX(m,recoil*.18);RY(m,.05);drawGun(m,ME.w,.62);DIM=1;
    const fl=fx.find(f=>f.k==='flash'&&f.me);if(fl){const f2=T(M4(),.16,-.155,-.3+MUZZLE[ME.w]*.62);RZ(f2,Math.random()*3);gl.enable(gl.BLEND);gl.blendFunc(gl.SRC_ALPHA,gl.ONE);cube(SC(f2,.12,.12,.04),[1,.75,.3],1,.9);gl.disable(gl.BLEND)}}
  render.S=S;render.V=mul(PROJ,V);render.e=e;
}

/* ---------- интерфейс ---------- */
function project(p){const m=render.V;const x=m[0]*p[0]+m[4]*p[1]+m[8]*p[2]+m[12],y=m[1]*p[0]+m[5]*p[1]+m[9]*p[2]+m[13],w=m[3]*p[0]+m[7]*p[1]+m[11]*p[2]+m[15];if(w<.1)return null;return[(x/w*.5+.5)*W,(1-(y/w*.5+.5))*H]}
const font=(s,b=700)=>`${b} ${s}px system-ui,-apple-system,Segoe UI,Roboto,sans-serif`;
function txt(t,x,y,s,c,al='left',b=700){hx.font=font(s,b);hx.textAlign=al;hx.lineWidth=3;hx.strokeStyle='rgba(0,0,0,.65)';hx.strokeText(t,x,y);hx.fillStyle=c;hx.fillText(t,x,y)}
let showScores=false;
function drawHUD(now){
  hx.clearRect(0,0,W,H);const sm=W<700;
  // имена
  if(render.S)for(const o of render.S.pl){if(o.id===myId||!o.alive)continue;const hp=[o.p[0],o.p[1]+2.15,o.p[2]];const dd=len(sub(hp,render.e));if(dd>40)continue;
    if(mapRay(render.e,nrm(sub(hp,render.e)),dd).hit)continue;const s=project(hp);if(!s)continue;const sc=scores.find(x=>x.id===o.id);if(sc)txt(sc.n,s[0],s[1],Math.max(11,15-dd/6),'#fff','center',600)}
  // урон
  dmgFlash=Math.max(0,dmgFlash-1/60*1.5);if(dmgFlash>0){const g=hx.createRadialGradient(W/2,H/2,Math.min(W,H)*.3,W/2,H/2,Math.max(W,H)*.7);g.addColorStop(0,'rgba(200,0,0,0)');g.addColorStop(1,`rgba(200,0,0,${dmgFlash*.6})`);hx.fillStyle=g;hx.fillRect(0,0,W,H)
    if(dmgDir&&P.alive){const d=sub(dmgDir,P.p);const a=Math.atan2(d[0],-d[2])+P.yaw;hx.save();hx.translate(W/2,H/2);hx.rotate(-a);hx.fillStyle=`rgba(255,40,30,${dmgFlash})`;hx.beginPath();hx.moveTo(0,-90);hx.lineTo(-18,-70);hx.lineTo(18,-70);hx.fill();hx.restore()}}
  if(P.alive){// прицел
    const cx=W/2,cy=H/2;hx.strokeStyle='rgba(255,255,255,.9)';hx.lineWidth=2;hx.beginPath();for(const[a,b,c,d]of[[-10,0,-4,0],[4,0,10,0],[0,-10,0,-4],[0,4,0,10]]){hx.moveTo(cx+a,cy+b);hx.lineTo(cx+c,cy+d)}hx.stroke();
    if(now-hitMark<180){hx.strokeStyle='#ff4a3a';hx.lineWidth=2.5;hx.beginPath();for(const s of[[1,1],[1,-1],[-1,1],[-1,-1]]){hx.moveTo(cx+s[0]*6,cy+s[1]*6);hx.lineTo(cx+s[0]*13,cy+s[1]*13)}hx.stroke()}
    // здоровье, броня, патроны
    const by=H-28,bl=coarse?Math.max(16,W*.02):24;
    txt(String(ME.hp),bl,by-12,sm?30:40,ME.hp<30?'#ff5040':'#fff');txt('ЗДОРОВЬЕ',bl,by+2,11,'#ffcf9a','left',600);
    txt(String(ME.ar),bl+(sm?70:100),by-12,sm?30:40,'#9fe08f');txt('БРОНЯ',bl+(sm?70:100),by+2,11,'#ffcf9a','left',600);
    const am=ammoOf(ME.w);const ar=W-24,ay=coarse?H-230:by;txt(am===Infinity?'∞':String(am),ar,ay,sm?30:40,'#fff','right');txt((WP[ME.w]||{}).n||'',ar,ay+14,11,'#ffcf9a','right',600);
    // слоты оружия
    if(!coarse||W>800){const sw=42,x0=W/2-sw*4.5,y0=H-58;for(let i=0;i<9;i++){const own=ME.has.includes(i),cur=i===ME.w;hx.fillStyle=cur?'rgba(255,150,40,.55)':own?'rgba(0,0,0,.4)':'rgba(0,0,0,.18)';hx.fillRect(x0+i*sw+2,y0,sw-4,34);
      txt(String(i+1),x0+i*sw+6,y0+13,11,own?'#fff':'#777','left',700);if(own&&i)txt(String(ammoOf(i)),x0+i*sw+sw-6,y0+29,11,'#ffcf9a','right',600)}}
  }else{txt(deathMsg||'Вы погибли',W/2,H/2-20,sm?24:32,'#ff6a50','center',800);txt('Возрождение…',W/2,H/2+14,16,'#fff','center',500)}
  // лента убийств
  let y=coarse?66:28;for(const f of feed){const a=1-clamp((now-f.t-6000)/1000,0,1);if(a<=0)continue;hx.globalAlpha=a;
    if(f.sys)txt(f.sys,W-14,y,13,'#c8b8a8','right',500);else txt(`${f.a||'☠'}  [${f.w}]  ${f.v}`,W-14,y,14,f.a===myName?'#ffd060':f.v===myName?'#ff7060':'#fff','right',600);y+=20}hx.globalAlpha=1;
  feed=feed.filter(f=>now-f.t<7000);
  // сообщения
  msgs=msgs.filter(m=>now-m.at<2200);msgs.forEach((m,i)=>{hx.globalAlpha=1-clamp((now-m.at-1600)/600,0,1);txt(m.t,W/2,H*.68+i*24,18,'#ffd28a','center',700)});hx.globalAlpha=1;
  // инфо
  const sorted=[...scores].sort((a,b)=>b.f-a.f);const place=sorted.findIndex(s=>s.id===myId)+1;
  txt(`Фраги: ${ME.f}  ·  место ${place||'-'}/${scores.length}`,coarse?66:14,coarse?34:24,14,'#fff','left',700);
  txt(`${fps} к/с · пинг ${Math.round(rtt)} мс`,coarse?66:14,coarse?52:42,11,'#bfa58c','left',500);
  if(showScores||(!P.alive&&now-dead0>800)){const w=Math.min(420,W-32),x=(W-w)/2,y0=Math.max(60,H*.18),rh=26;hx.fillStyle='rgba(10,6,4,.78)';hx.fillRect(x,y0,w,40+rh*sorted.length);
    txt('ИГРОК',x+14,y0+24,12,'#ffcf9a','left',700);txt('ФРАГИ',x+w-90,y0+24,12,'#ffcf9a','right',700);txt('СМЕРТИ',x+w-14,y0+24,12,'#ffcf9a','right',700);
    sorted.forEach((s,i)=>{const yy=y0+50+i*rh,c=s.id===myId?'#ffd060':'#fff';const cc=COLORS[s.c%8];hx.fillStyle=`rgb(${cc.map(v=>v*255|0)})`;hx.fillRect(x+14,yy-10,10,10);
      txt(s.n,x+32,yy,14,c,'left',600);txt(String(s.f),x+w-90,yy,14,c,'right',700);txt(String(s.d),x+w-14,yy,14,c,'right',500)})}
  if(coarse&&H>W)txt('Поверните устройство горизонтально',W/2,H*.3,18,'#ffd28a','center',700);
}

/* ---------- управление ---------- */
let sens=+(localStorage.getItem('arena.sens')||1);$('#sens').value=sens;$('#sens').oninput=e=>{sens=+e.target.value;try{localStorage.setItem('arena.sens',sens)}catch(_){}};
const locked=()=>document.pointerLockElement===cv;
function look(dx,dy,k){k*=aimFric;P.yaw-=dx*k*sens;P.pitch=clamp(P.pitch-dy*k*sens,-1.5,1.5)}
/* лёгкая автодоводка для сенсорных экранов: замедляет обзор над противником и слегка подтягивает прицел, пока игрок целится или стреляет */
let aimOn=coarse&&(()=>{try{return localStorage.getItem('arena.aim')!=='0'}catch(e){return true}})(),aimFric=1,aimTgt=0;
function aimAssist(dt){aimFric=1;if(!aimOn||!P.alive||!render.S)return;const e=eye(),f=fwdOf(P.yaw,P.pitch);let best=null,bs=1e9,bid=0;
  for(const o of render.S.pl){if(o.id===myId||!o.alive)continue;const c=[o.p[0],o.p[1]+1.2,o.p[2]],d=sub(c,e),L=len(d);if(L<1||L>60)continue;const n=scl(d,1/L);
    let lim=clamp(Math.atan(1.6/L)*2,.07,.24);if(o.id===aimTgt)lim*=1.3; // «липкость»: текущую цель отпускаем не сразу
    const ang=Math.acos(clamp(dot(n,f),-1,1));if(ang>lim||mapRay(e,n,L-.5).hit)continue;const sc=ang/lim;if(sc<bs){bs=sc;best=n;bid=o.id}}
  aimTgt=bid;if(!best)return;aimFric=.42+.58*bs; // над целью обзор заметно медленнее
  if(!(firing||TC.lk||TC.fire||Math.abs(mvx)+Math.abs(mvy)>.2))return;
  const rate=firing?6:3.5,maxV=2.4*dt; // плавно, без рывков: не быстрее ~140°/с
  let dy=angLerp(0,Math.atan2(-best[0],-best[2])-P.yaw,1),dp=Math.asin(clamp(best[1],-1,1))-P.pitch;
  dy=clamp(dy*Math.min(1,rate*dt),-maxV,maxV);dp=clamp(dp*Math.min(1,rate*dt),-maxV,maxV);P.yaw+=dy;P.pitch=clamp(P.pitch+dp,-1.5,1.5)}
{const other=MAPKEY==='town'?'arena':'town';$('#other').textContent='Перейти на карту «'+(other==='town'?'Городок':'Арена')+'»';$('#other').onclick=()=>{try{ws&&ws.close()}catch(e){}location.href='/game?map='+other};
  if(coarse){$('#aimL').hidden=false;$('#aim').checked=aimOn;$('#aim').onchange=e=>{aimOn=e.target.checked;try{localStorage.setItem('arena.aim',aimOn?'1':'0')}catch(_){}}}}
function showMenu(){$('#menu').style.display='flex';$('#tc').classList.remove('on');firing=false;for(const k in K)K[k]=false;mvx=mvy=0}
function hideMenu(){$('#menu').style.display='none';if(coarse)$('#tc').classList.add('on')}
$('#help').innerHTML=coarse?'<b>Левая половина экрана</b> — ходьба (джойстик)<br><b>Правая половина</b> — обзор<br><b>ОГОНЬ</b> — стрелять (можно вести пальцем)<br><b>⟳</b> — сменить оружие · <b>☰</b> — счёт':
 '<b>WASD</b> — ходьба · <b>Пробел</b> — прыжок · <b>Мышь</b> — обзор и огонь<br><b>1–9</b> или колесо — оружие · <b>Q</b> — предыдущее<br><b>Tab</b> — счёт · <b>Esc</b> — меню<br>Ракета под ноги + прыжок = рокет-джамп';
$('#play').onclick=()=>{initAudio();if(AC&&AC.state==='suspended')AC.resume();if(coarse){try{document.documentElement.requestFullscreen&&document.documentElement.requestFullscreen().catch(()=>{});screen.orientation&&screen.orientation.lock&&screen.orientation.lock('landscape').catch(()=>{})}catch(e){}hideMenu()}
  else{const r=cv.requestPointerLock();if(r&&r.catch)r.catch(()=>{})}};
$('#exit').onclick=()=>{try{ws&&ws.close()}catch(e){}location.href='/'};
document.addEventListener('pointerlockchange',()=>{if(locked())hideMenu();else showMenu()});
document.addEventListener('mousemove',e=>{if(locked())look(e.movementX,e.movementY,.0022)});
document.addEventListener('mousedown',e=>{if(!locked())return;if(e.button===0)firing=true;if(e.button===2)cycle(1)});
document.addEventListener('mouseup',e=>{if(e.button===0)firing=false});
document.addEventListener('contextmenu',e=>e.preventDefault());
document.addEventListener('wheel',e=>{if(locked())cycle(e.deltaY>0?1:-1)},{passive:true});
document.addEventListener('keydown',e=>{if(!locked()&&!coarse)return;K[e.code]=true;if(e.code==='Tab'){showScores=true;e.preventDefault()}
  if(/^Digit[1-9]$/.test(e.code))setWeapon(+e.code[5]-1);if(e.code==='KeyQ')setWeapon(ME.prevW);if(e.code==='Space')e.preventDefault()});
document.addEventListener('keyup',e=>{K[e.code]=false;if(e.code==='Tab')showScores=false});
addEventListener('blur',()=>{for(const k in K)K[k]=false;firing=false});
// сенсорное управление
const TC={mv:null,lk:null,fire:null};const js=$('#js'),knob=js.querySelector('i');
const onBtn=(id,down,up)=>{const b=$(id);b.addEventListener('touchstart',e=>{e.preventDefault();e.stopPropagation();b.classList.add('on');down(e)},{passive:false});
  b.addEventListener('touchend',e=>{e.preventDefault();b.classList.remove('on');up&&up(e)},{passive:false});b.addEventListener('touchcancel',()=>{b.classList.remove('on');up&&up()})};
onBtn('#bFire',e=>{initAudio();firing=true;const t=e.changedTouches[0];TC.fire={id:t.identifier,x:t.clientX,y:t.clientY}},()=>{firing=false;TC.fire=null});
onBtn('#bJump',()=>{jumpQ=true});onBtn('#bNext',()=>cycle(1));onBtn('#bScore',()=>{showScores=!showScores});onBtn('#bMenu',()=>{showMenu();try{document.exitFullscreen&&document.fullscreenElement&&document.exitFullscreen()}catch(e){}});
$('#tc').addEventListener('touchstart',e=>{e.preventDefault();initAudio();for(const t of e.changedTouches){if(t.clientX<W*.45&&!TC.mv){TC.mv={id:t.identifier,x:t.clientX,y:t.clientY};js.style.display='block';js.style.left=t.clientX+'px';js.style.top=t.clientY+'px';knob.style.transform=''}
  else if(!TC.lk)TC.lk={id:t.identifier,x:t.clientX,y:t.clientY}}},{passive:false});
document.addEventListener('touchmove',e=>{if($('#menu').style.display!=='none')return;e.preventDefault();for(const t of e.changedTouches){
  if(TC.mv&&t.identifier===TC.mv.id){let dx=t.clientX-TC.mv.x,dy=t.clientY-TC.mv.y;const l=Math.hypot(dx,dy),R=55;if(l>R){dx*=R/l;dy*=R/l}knob.style.transform=`translate(${dx}px,${dy}px)`;mvx=dx/R;mvy=dy/R}
  for(const k of['lk','fire'])if(TC[k]&&t.identifier===TC[k].id){look(t.clientX-TC[k].x,t.clientY-TC[k].y,.0055);TC[k].x=t.clientX;TC[k].y=t.clientY}}},{passive:false});
const tend=e=>{for(const t of e.changedTouches){if(TC.mv&&t.identifier===TC.mv.id){TC.mv=null;mvx=mvy=0;js.style.display='none'}if(TC.lk&&t.identifier===TC.lk.id)TC.lk=null}};
document.addEventListener('touchend',tend);document.addEventListener('touchcancel',tend);

/* ---------- запуск ---------- */
try{initGL();makeTextures();makeCube();resize();connect();requestAnimationFrame(frame)}catch(e){fatal('Не удалось запустить игру: '+e.message)}
